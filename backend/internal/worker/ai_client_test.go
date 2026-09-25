package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/opsconfig"
)

type fakeAISource struct {
	mu      sync.Mutex
	flags   opsconfig.Flags
	config  opsconfig.AI
	flagErr error
}

type fakeRoutedAISource struct {
	*fakeAISource
	providers map[string]opsconfig.AIProvider
	routes    map[opsconfig.ModelPurpose]opsconfig.ModelRoute
}

func (s *fakeRoutedAISource) ModelRoute(_ context.Context, purpose opsconfig.ModelPurpose) (opsconfig.AIProvider, opsconfig.ModelRoute, error) {
	route, ok := s.routes[purpose]
	if !ok {
		return opsconfig.AIProvider{}, opsconfig.ModelRoute{}, opsconfig.ErrModelRouteNotFound
	}
	provider, ok := s.providers[route.ProviderID]
	if !ok {
		return opsconfig.AIProvider{}, opsconfig.ModelRoute{}, opsconfig.ErrModelRouteNotFound
	}
	return provider, route, nil
}

func (s *fakeRoutedAISource) AIProvider(_ context.Context, id string) (opsconfig.AIProvider, error) {
	provider, ok := s.providers[id]
	if !ok {
		return opsconfig.AIProvider{}, opsconfig.ErrModelRouteNotFound
	}
	return provider, nil
}

func (s *fakeAISource) Flags(context.Context) (opsconfig.Flags, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flags, s.flagErr
}

func (s *fakeAISource) AI(context.Context) (opsconfig.AI, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config, nil
}

func TestRuntimeAIClientHonorsDynamicKillSwitch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if got := request.Header.Get("Authorization"); got != "Bearer provider-secret-key" {
			t.Errorf("authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id": "test", "object": "chat.completion", "created": 1, "model": "text-model",
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": `{"ok":true}`}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer server.Close()

	source := &fakeAISource{flags: opsconfig.Flags{AIEnabled: true}, config: opsconfig.AI{
		BaseURL: server.URL, TextModel: "text-model", VisionModel: "vision-model",
	}}
	client := newRuntimeAIClient(source, nil, opsconfig.AI{APIKey: "provider-secret-key"}, false, true, 5*time.Second)
	if _, err := client.Call(context.Background(), llm.Request{Model: "text-model", Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	source.flags.AIEnabled = false
	source.mu.Unlock()
	if _, err := client.Call(context.Background(), llm.Request{Model: "text-model", Prompt: "test"}); !errors.Is(err, llm.ErrDisabled) {
		t.Fatalf("disabled call error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

func TestRuntimeAIClientRoutesPurposesToIndependentProviders(t *testing.T) {
	type seenRequest struct {
		Authorization string
		Model         string
		Temperature   float64
		MaxTokens     int64
	}
	seen := make(chan seenRequest, 2)
	providerServer := func(responseModel string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var body struct {
				Model       string  `json:"model"`
				Temperature float64 `json:"temperature"`
				MaxTokens   int64   `json:"max_tokens"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			seen <- seenRequest{Authorization: request.Header.Get("Authorization"), Model: body.Model, Temperature: body.Temperature, MaxTokens: body.MaxTokens}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id": "test", "object": "chat.completion", "created": 1, "model": responseModel,
				"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": `{"ok":true}`}, "finish_reason": "stop"}},
				"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
		}))
	}
	textServer := providerServer("provider-text-model")
	defer textServer.Close()
	visionServer := providerServer("provider-vision-model")
	defer visionServer.Close()

	cipher, err := opsconfig.NewCipher("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	textKey, _ := cipher.Seal("text-secret-key")
	visionKey, _ := cipher.Seal("vision-secret-key")
	source := &fakeRoutedAISource{
		fakeAISource: &fakeAISource{flags: opsconfig.Flags{AIEnabled: true}, config: opsconfig.DefaultAI()},
		providers: map[string]opsconfig.AIProvider{
			"text": {
				ID: "text", Name: "Text vendor", BaseURL: textServer.URL, AuthType: "bearer", APIKey: textKey,
				TimeoutSeconds: 5, Enabled: true, Capabilities: opsconfig.ProviderCapabilities{JSON: true, Stream: true},
			},
			"vision": {
				ID: "vision", Name: "Vision vendor", BaseURL: visionServer.URL, AuthType: "bearer", APIKey: visionKey,
				TimeoutSeconds: 5, Enabled: true, Capabilities: opsconfig.ProviderCapabilities{JSON: true, Vision: true},
			},
		},
		routes: map[opsconfig.ModelPurpose]opsconfig.ModelRoute{
			opsconfig.PurposeMaterialCompose: {Purpose: opsconfig.PurposeMaterialCompose, ProviderID: "text", Model: "text-model", Parameters: map[string]any{"temperature": 0.4, "maxTokens": 3072}},
			opsconfig.PurposeMaterialVision:  {Purpose: opsconfig.PurposeMaterialVision, ProviderID: "vision", Model: "vision-model"},
		},
	}
	client := newRuntimeAIClient(source, cipher, opsconfig.AI{}, false, true, 5*time.Second)
	textResponse, err := client.Call(context.Background(), llm.Request{
		Purpose: llm.PurposeMaterialCompose, Prompt: "test", JSON: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	visionResponse, err := client.Call(context.Background(), llm.Request{
		Purpose: llm.PurposeMaterialVision, Prompt: "read", Image: &llm.Image{MediaType: "image/png", Data: []byte("png")}, JSON: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, second := <-seen, <-seen
	if first.Authorization != "Bearer text-secret-key" || first.Model != "text-model" || first.Temperature != 0.4 || first.MaxTokens != 3072 {
		t.Fatalf("text route = %#v", first)
	}
	if second.Authorization != "Bearer vision-secret-key" || second.Model != "vision-model" {
		t.Fatalf("vision route = %#v", second)
	}
	if textResponse.Provider != "Text vendor" || textResponse.Purpose != llm.PurposeMaterialCompose {
		t.Fatalf("text response route metadata = %#v", textResponse)
	}
	if visionResponse.Provider != "Vision vendor" || visionResponse.Purpose != llm.PurposeMaterialVision {
		t.Fatalf("vision response route metadata = %#v", visionResponse)
	}
}
