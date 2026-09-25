package opsconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"easygpa/backend/internal/llm"
)

// ModelPurpose is the stable business vocabulary used by callers. Provider
// names and model IDs belong to operations, not to business packages.
type ModelPurpose string

const (
	PurposeMaterialVision  ModelPurpose = llm.PurposeMaterialVision
	PurposeMaterialCompose ModelPurpose = llm.PurposeMaterialCompose
	PurposeKnowledgeOCR    ModelPurpose = llm.PurposeKnowledgeOCR
	PurposeAgentText       ModelPurpose = llm.PurposeAgentText
	PurposeAgentVision     ModelPurpose = llm.PurposeAgentVision
)

var ModelPurposes = []ModelPurpose{
	PurposeMaterialVision,
	PurposeMaterialCompose,
	PurposeKnowledgeOCR,
	PurposeAgentText,
	PurposeAgentVision,
}

func NormalizeModelPurpose(raw string) (ModelPurpose, error) {
	purpose := ModelPurpose(strings.ToLower(strings.TrimSpace(raw)))
	for _, allowed := range ModelPurposes {
		if purpose == allowed {
			return purpose, nil
		}
	}
	return "", fmt.Errorf("不支持的业务模型槽位：%s", raw)
}

func (p ModelPurpose) RequiresVision() bool {
	return p == PurposeMaterialVision || p == PurposeKnowledgeOCR || p == PurposeAgentVision
}

type ProviderCapabilities struct {
	JSON   bool `json:"json"`
	Stream bool `json:"stream"`
	Vision bool `json:"vision"`
	Models bool `json:"models"`
}

type AIProvider struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	BaseURL        string               `json:"baseUrl"`
	AuthType       string               `json:"authType"`
	APIKey         string               `json:"-"`
	TimeoutSeconds int                  `json:"timeoutSeconds"`
	MaxRetries     int                  `json:"maxRetries"`
	Capabilities   ProviderCapabilities `json:"capabilities"`
	Enabled        bool                 `json:"enabled"`
	Revision       int64                `json:"revision"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
	APIKeySet      bool                 `json:"apiKeySet"`
}

type ModelRoute struct {
	Purpose      ModelPurpose   `json:"purpose"`
	ProviderID   string         `json:"providerId"`
	ProviderName string         `json:"providerName,omitempty"`
	Model        string         `json:"model"`
	Parameters   map[string]any `json:"parameters"`
	Revision     int64          `json:"revision"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

type ModelBinding struct {
	Purpose          ModelPurpose
	ProviderID       string
	ProviderName     string
	BaseURL          string
	APIKey           string
	AuthType         string
	Model            string
	Timeout          time.Duration
	MaxRetries       int
	Capabilities     ProviderCapabilities
	Parameters       map[string]any
	ProviderRevision int64
	RouteRevision    int64
}

// RouteSnapshot is safe to persist with tenant jobs: it identifies the endpoint
// and model chosen at enqueue time but never contains credentials.
type RouteSnapshot struct {
	Purpose          ModelPurpose   `json:"purpose"`
	ProviderID       string         `json:"providerId,omitempty"`
	ProviderName     string         `json:"providerName,omitempty"`
	BaseURL          string         `json:"baseUrl"`
	Model            string         `json:"model"`
	ProviderRevision int64          `json:"providerRevision,omitempty"`
	RouteRevision    int64          `json:"routeRevision,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	Legacy           bool           `json:"legacy,omitempty"`
}

func (b ModelBinding) Snapshot() RouteSnapshot {
	return RouteSnapshot{
		Purpose: b.Purpose, ProviderID: b.ProviderID, ProviderName: b.ProviderName,
		BaseURL: b.BaseURL, Model: b.Model, ProviderRevision: b.ProviderRevision,
		RouteRevision: b.RouteRevision, Parameters: cloneModelParameters(b.Parameters),
	}
}

// NormalizeModelParameters is deliberately a small allowlist. Arbitrary JSON
// must never leak through to vendor-specific request bodies because it would
// make routes non-portable and bypass business-owned safety controls.
func NormalizeModelParameters(input map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	for key, raw := range input {
		switch key {
		case "temperature":
			value, ok := modelNumber(raw)
			if !ok || value < 0 || value > 2 {
				return nil, errors.New("temperature 必须是 0—2 的数字")
			}
			result[key] = value
		case "maxTokens":
			value, ok := modelNumber(raw)
			if !ok || value < 1 || value > 131072 || value != float64(int64(value)) {
				return nil, errors.New("maxTokens 必须是 1—131072 的整数")
			}
			result[key] = int64(value)
		default:
			return nil, fmt.Errorf("不支持的模型路由参数：%s", key)
		}
	}
	return result, nil
}

func modelNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func cloneModelParameters(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (b ModelBinding) ApplyParameters(request *llm.Request) {
	if request == nil {
		return
	}
	if value, ok := modelNumber(b.Parameters["temperature"]); ok {
		request.Temperature = value
	}
	if value, ok := modelNumber(b.Parameters["maxTokens"]); ok {
		request.MaxTokens = int64(value)
	}
}

var ErrModelRouteNotFound = errors.New("model route not configured")

type ModelRouteSource interface {
	ModelRoute(context.Context, ModelPurpose) (AIProvider, ModelRoute, error)
	AIProvider(context.Context, string) (AIProvider, error)
}

func (s *Store) AIProviders(ctx context.Context) ([]AIProvider, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("ops config database is unavailable")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text,name,base_url,auth_type,api_key,timeout_seconds,max_retries,
		       capabilities,enabled,revision,created_at,updated_at
		  FROM ai_provider ORDER BY lower(name),id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	providers := make([]AIProvider, 0)
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

func (s *Store) AIProvider(ctx context.Context, id string) (AIProvider, error) {
	if s == nil || s.pool == nil {
		return AIProvider{}, errors.New("ops config database is unavailable")
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id::text,name,base_url,auth_type,api_key,timeout_seconds,max_retries,
		       capabilities,enabled,revision,created_at,updated_at
		  FROM ai_provider WHERE id=$1::uuid
	`, id)
	provider, err := scanProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AIProvider{}, ErrModelRouteNotFound
	}
	return provider, err
}

func (s *Store) ModelRoutes(ctx context.Context) ([]ModelRoute, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("ops config database is unavailable")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.purpose,r.provider_id::text,p.name,r.model,r.parameters,r.revision,r.updated_at
		  FROM ai_model_route r JOIN ai_provider p ON p.id=r.provider_id
		 ORDER BY r.purpose
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := make([]ModelRoute, 0)
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (s *Store) ModelRoute(ctx context.Context, purpose ModelPurpose) (AIProvider, ModelRoute, error) {
	if s == nil || s.pool == nil {
		return AIProvider{}, ModelRoute{}, errors.New("ops config database is unavailable")
	}
	row := s.pool.QueryRow(ctx, `
		SELECT p.id::text,p.name,p.base_url,p.auth_type,p.api_key,p.timeout_seconds,p.max_retries,
		       p.capabilities,p.enabled,p.revision,p.created_at,p.updated_at,
		       r.purpose,r.provider_id::text,p.name,r.model,r.parameters,r.revision,r.updated_at
		  FROM ai_model_route r JOIN ai_provider p ON p.id=r.provider_id
		 WHERE r.purpose=$1
	`, purpose)
	var provider AIProvider
	var capabilities, parameters []byte
	var route ModelRoute
	err := row.Scan(
		&provider.ID, &provider.Name, &provider.BaseURL, &provider.AuthType, &provider.APIKey,
		&provider.TimeoutSeconds, &provider.MaxRetries, &capabilities, &provider.Enabled,
		&provider.Revision, &provider.CreatedAt, &provider.UpdatedAt,
		&route.Purpose, &route.ProviderID, &route.ProviderName, &route.Model, &parameters,
		&route.Revision, &route.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AIProvider{}, ModelRoute{}, ErrModelRouteNotFound
	}
	if err != nil {
		return AIProvider{}, ModelRoute{}, err
	}
	provider.APIKeySet = strings.TrimSpace(provider.APIKey) != ""
	if err := json.Unmarshal(capabilities, &provider.Capabilities); err != nil {
		return AIProvider{}, ModelRoute{}, err
	}
	if err := json.Unmarshal(parameters, &route.Parameters); err != nil {
		return AIProvider{}, ModelRoute{}, err
	}
	return provider, route, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanProvider(row rowScanner) (AIProvider, error) {
	var provider AIProvider
	var capabilities []byte
	if err := row.Scan(
		&provider.ID, &provider.Name, &provider.BaseURL, &provider.AuthType, &provider.APIKey,
		&provider.TimeoutSeconds, &provider.MaxRetries, &capabilities, &provider.Enabled,
		&provider.Revision, &provider.CreatedAt, &provider.UpdatedAt,
	); err != nil {
		return AIProvider{}, err
	}
	provider.APIKeySet = strings.TrimSpace(provider.APIKey) != ""
	if err := json.Unmarshal(capabilities, &provider.Capabilities); err != nil {
		return AIProvider{}, err
	}
	return provider, nil
}

func scanRoute(row rowScanner) (ModelRoute, error) {
	var route ModelRoute
	var parameters []byte
	if err := row.Scan(&route.Purpose, &route.ProviderID, &route.ProviderName, &route.Model, &parameters, &route.Revision, &route.UpdatedAt); err != nil {
		return ModelRoute{}, err
	}
	if err := json.Unmarshal(parameters, &route.Parameters); err != nil {
		return ModelRoute{}, err
	}
	return route, nil
}

func ResolveModelBinding(provider AIProvider, route ModelRoute, cipher *Cipher, allowLoopbackHTTP bool) (ModelBinding, error) {
	if !provider.Enabled {
		return ModelBinding{}, errors.New("模型供应商已停用")
	}
	if route.Purpose.RequiresVision() && !provider.Capabilities.Vision {
		return ModelBinding{}, errors.New("模型供应商未声明视觉能力")
	}
	baseURL, err := NormalizeAIBaseURL(provider.BaseURL, allowLoopbackHTTP)
	if err != nil {
		return ModelBinding{}, err
	}
	model, err := NormalizeAIModel(route.Model)
	if err != nil {
		return ModelBinding{}, err
	}
	parameters, err := NormalizeModelParameters(route.Parameters)
	if err != nil {
		return ModelBinding{}, err
	}
	if strings.ToLower(strings.TrimSpace(provider.AuthType)) != "bearer" {
		return ModelBinding{}, errors.New("模型供应商鉴权方式不受支持")
	}
	if !IsSealed(provider.APIKey) {
		return ModelBinding{}, errors.New("模型供应商 API Key 未安全配置")
	}
	if cipher == nil {
		return ModelBinding{}, errors.New("AI_CONFIG_SECRET_KEY 未配置，无法解密模型供应商 API Key")
	}
	key, err := cipher.Open(provider.APIKey)
	if err != nil {
		return ModelBinding{}, err
	}
	key, err = NormalizeAIAPIKey(key)
	if err != nil {
		return ModelBinding{}, err
	}
	timeout := provider.TimeoutSeconds
	if timeout <= 0 {
		timeout = 90
	}
	return ModelBinding{
		Purpose: route.Purpose, ProviderID: provider.ID, ProviderName: provider.Name,
		BaseURL: baseURL, APIKey: key, AuthType: "bearer", Model: model,
		Timeout: time.Duration(timeout) * time.Second, MaxRetries: provider.MaxRetries,
		Capabilities: provider.Capabilities, ProviderRevision: provider.Revision,
		RouteRevision: route.Revision, Parameters: parameters,
	}, nil
}

func LegacyModelBinding(runtime AIRuntime, purpose ModelPurpose, timeout time.Duration) (ModelBinding, error) {
	model := runtime.TextModel
	switch purpose {
	case PurposeMaterialVision, PurposeKnowledgeOCR, PurposeAgentVision:
		model = runtime.VisionModel
	case PurposeAgentText:
		model = runtime.AgentModel
	case PurposeMaterialCompose:
	default:
		return ModelBinding{}, ErrModelRouteNotFound
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return ModelBinding{
		Purpose: purpose, ProviderName: "Legacy default", BaseURL: runtime.BaseURL,
		APIKey: runtime.APIKey, AuthType: "bearer", Model: model, Timeout: timeout,
		Capabilities: ProviderCapabilities{JSON: true, Stream: true, Vision: true},
	}, nil
}
