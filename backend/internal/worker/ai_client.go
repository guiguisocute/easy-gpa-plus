package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"sync"
	"time"

	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/opsconfig"
)

type aiRuntimeSource interface {
	Flags(context.Context) (opsconfig.Flags, error)
	AI(context.Context) (opsconfig.AI, error)
}

// runtimeAIClient resolves encrypted operational settings for every model call.
// The Store's short cache keeps this cheap while allowing URL/key changes and
// the kill switch to take effect without restarting the worker.
type runtimeAIClient struct {
	source            aiRuntimeSource
	cipher            *opsconfig.Cipher
	fallback          opsconfig.AI
	fallbackEnabled   bool
	allowLoopbackHTTP bool
	timeout           time.Duration

	mu      sync.Mutex
	clients map[[32]byte]llm.Client
}

func newRuntimeAIClient(source aiRuntimeSource, cipher *opsconfig.Cipher, fallback opsconfig.AI, fallbackEnabled, allowLoopbackHTTP bool, timeout time.Duration) llm.Client {
	return &runtimeAIClient{
		source: source, cipher: cipher, fallback: fallback, fallbackEnabled: fallbackEnabled,
		allowLoopbackHTTP: allowLoopbackHTTP, timeout: timeout, clients: make(map[[32]byte]llm.Client),
	}
}

func (c *runtimeAIClient) Call(ctx context.Context, request llm.Request) (llm.Response, error) {
	client, binding, routed, err := c.resolve(ctx, request)
	if err != nil {
		return llm.Response{}, err
	}
	response, err := client.Call(ctx, routed)
	return annotateResponse(response, binding), err
}

// Stream 让 runtimeAIClient 同样满足 llm.StreamingClient。少了它，agentjob 的
// 类型断言会静默退回 Call —— 逐字输出不会报错，只是永远不发生。
func (c *runtimeAIClient) Stream(ctx context.Context, request llm.Request, onDelta func(llm.Delta) error) (llm.Response, error) {
	client, binding, routed, err := c.resolve(ctx, request)
	if err != nil {
		return llm.Response{}, err
	}
	streaming, ok := client.(llm.StreamingClient)
	if !ok {
		response, err := client.Call(ctx, routed)
		return annotateResponse(response, binding), err
	}
	response, err := streaming.Stream(ctx, routed, onDelta)
	return annotateResponse(response, binding), err
}

// resolve chooses a provider by route snapshot or business purpose. Legacy
// single-provider settings remain the fallback until explicit routes exist.
func (c *runtimeAIClient) resolve(ctx context.Context, request llm.Request) (llm.Client, opsconfig.ModelBinding, llm.Request, error) {
	stored := opsconfig.DefaultAI()
	enabled := c.fallbackEnabled
	if c.source != nil {
		flags, err := c.source.Flags(ctx)
		if err != nil {
			return nil, opsconfig.ModelBinding{}, request, err
		}
		enabled = flags.AIEnabled
		stored, err = c.source.AI(ctx)
		if err != nil {
			return nil, opsconfig.ModelBinding{}, request, err
		}
	}
	if !enabled {
		return nil, opsconfig.ModelBinding{}, request, llm.ErrDisabled
	}

	purpose, purposeErr := opsconfig.NormalizeModelPurpose(request.Purpose)
	if request.Purpose == "" {
		purposeErr = opsconfig.ErrModelRouteNotFound
	}
	var binding opsconfig.ModelBinding
	var err error
	if request.Route != nil {
		binding, err = c.bindingFromSnapshot(ctx, *request.Route, purpose, stored)
	} else if purposeErr == nil {
		binding, err = c.bindingForPurpose(ctx, purpose, stored)
	} else {
		binding, err = c.legacyBinding(stored, purpose, request.Model)
	}
	if err != nil {
		return nil, opsconfig.ModelBinding{}, request, err
	}
	request.Model = binding.Model
	request.Purpose = string(binding.Purpose)
	binding.ApplyParameters(&request)
	if request.Route == nil {
		snapshot := binding.Snapshot()
		request.Route = &llm.Route{
			Purpose: string(snapshot.Purpose), ProviderID: snapshot.ProviderID, ProviderName: snapshot.ProviderName,
			BaseURL: snapshot.BaseURL, Model: snapshot.Model, ProviderRevision: snapshot.ProviderRevision,
			RouteRevision: snapshot.RouteRevision, Parameters: snapshot.Parameters, Legacy: snapshot.Legacy,
		}
	}
	fingerprint := sha256.Sum256([]byte(
		binding.BaseURL + "\x00" + binding.APIKey + "\x00" +
			binding.Timeout.String() + "\x00" + strconv.Itoa(binding.MaxRetries),
	))

	c.mu.Lock()
	client := c.clients[fingerprint]
	if client == nil {
		client, err = llm.NewOpenAICompatible(llm.Config{
			BaseURL: binding.BaseURL, APIKey: binding.APIKey, Timeout: binding.Timeout, MaxRetries: binding.MaxRetries,
			AllowPrivateNetwork: c.allowLoopbackHTTP,
		})
		if err != nil {
			c.mu.Unlock()
			return nil, opsconfig.ModelBinding{}, request, err
		}
		c.clients[fingerprint] = client
	}
	c.mu.Unlock()
	if client == nil {
		return nil, opsconfig.ModelBinding{}, request, errors.New("AI model client is unavailable")
	}
	return client, binding, request, nil
}

func (c *runtimeAIClient) bindingForPurpose(ctx context.Context, purpose opsconfig.ModelPurpose, stored opsconfig.AI) (opsconfig.ModelBinding, error) {
	if routes, ok := c.source.(opsconfig.ModelRouteSource); ok {
		provider, route, err := routes.ModelRoute(ctx, purpose)
		if err == nil {
			return opsconfig.ResolveModelBinding(provider, route, c.cipher, c.allowLoopbackHTTP)
		}
		if !errors.Is(err, opsconfig.ErrModelRouteNotFound) {
			return opsconfig.ModelBinding{}, err
		}
	}
	return c.legacyBinding(stored, purpose, "")
}

func (c *runtimeAIClient) bindingFromSnapshot(ctx context.Context, snapshot llm.Route, fallbackPurpose opsconfig.ModelPurpose, stored opsconfig.AI) (opsconfig.ModelBinding, error) {
	purpose, err := opsconfig.NormalizeModelPurpose(snapshot.Purpose)
	if err != nil {
		purpose = fallbackPurpose
	}
	if snapshot.ProviderID != "" {
		if routes, ok := c.source.(opsconfig.ModelRouteSource); ok {
			provider, err := routes.AIProvider(ctx, snapshot.ProviderID)
			if err != nil {
				return opsconfig.ModelBinding{}, err
			}
			// Endpoint and model are frozen; the encrypted key, enabled flag and
			// operational transport limits are intentionally current.
			provider.BaseURL = snapshot.BaseURL
			route := opsconfig.ModelRoute{
				Purpose: purpose, ProviderID: provider.ID, ProviderName: provider.Name,
				Model: snapshot.Model, Revision: snapshot.RouteRevision, Parameters: snapshot.Parameters,
			}
			return opsconfig.ResolveModelBinding(provider, route, c.cipher, c.allowLoopbackHTTP)
		}
	}
	binding, err := c.legacyBinding(stored, purpose, snapshot.Model)
	if err != nil {
		return opsconfig.ModelBinding{}, err
	}
	if snapshot.BaseURL != "" {
		binding.BaseURL = snapshot.BaseURL
	}
	return binding, nil
}

func (c *runtimeAIClient) legacyBinding(stored opsconfig.AI, purpose opsconfig.ModelPurpose, explicitModel string) (opsconfig.ModelBinding, error) {
	runtime, err := opsconfig.ResolveAI(stored, c.cipher, c.fallback, c.allowLoopbackHTTP)
	if err != nil {
		return opsconfig.ModelBinding{}, err
	}
	if purpose == "" {
		purpose = opsconfig.PurposeMaterialCompose
	}
	binding, err := opsconfig.LegacyModelBinding(runtime, purpose, c.timeout)
	if err != nil {
		return opsconfig.ModelBinding{}, err
	}
	if explicitModel != "" {
		binding.Model = explicitModel
	}
	return binding, nil
}

func annotateResponse(response llm.Response, binding opsconfig.ModelBinding) llm.Response {
	response.Purpose = string(binding.Purpose)
	response.ProviderID = binding.ProviderID
	response.Provider = binding.ProviderName
	return response
}
