package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/opsconfig"
)

type opsProviderInput struct {
	Name           *string                         `json:"name"`
	BaseURL        *string                         `json:"baseUrl"`
	APIKey         *string                         `json:"apiKey"`
	TimeoutSeconds *int                            `json:"timeoutSeconds"`
	MaxRetries     *int                            `json:"maxRetries"`
	Capabilities   *opsconfig.ProviderCapabilities `json:"capabilities"`
	Enabled        *bool                           `json:"enabled"`
}

func (s *Server) opsAIProviders(c *gin.Context) {
	store, ok := s.opsModelStore(c)
	if !ok {
		return
	}
	providers, err := store.AIProviders(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "ai_provider.list", "ai_provider", "", gin.H{"count": len(providers)}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": providers})
}

func (s *Server) createOpsAIProvider(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input opsProviderInput
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "模型供应商参数不正确", nil)
		return
	}
	if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" && s.cfg.AppEnv == "prod" && !requestUsesHTTPS(c.Request, s.cfg.TrustedProxies) {
		writeError(c, http.StatusUpgradeRequired, "https_required", "生产环境只能通过 HTTPS 保存 API Key", nil)
		return
	}
	provider, err := s.normalizeProviderInput(input, opsconfig.AIProvider{})
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "ai_provider_invalid", err.Error(), nil)
		return
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		writeError(c, http.StatusUnprocessableEntity, "ai_provider_invalid", "新建模型供应商必须配置 API Key", nil)
		return
	}
	rawCapabilities, _ := json.Marshal(provider.Capabilities)
	var id string
	err = s.deps.Pools.Ops.QueryRow(c.Request.Context(), `
		INSERT INTO ai_provider
		    (name,base_url,auth_type,api_key,timeout_seconds,max_retries,capabilities,enabled)
		VALUES ($1,$2,'bearer',$3,$4,$5,$6,$7) RETURNING id::text
	`, provider.Name, provider.BaseURL, provider.APIKey, provider.TimeoutSeconds,
		provider.MaxRetries, rawCapabilities, provider.Enabled).Scan(&id)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "ai_provider_exists", "模型供应商名称已存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "ai_provider.created", "ai_provider", id, gin.H{"name": provider.Name, "baseUrl": provider.BaseURL}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (s *Server) updateOpsAIProvider(c *gin.Context) {
	store, ok := s.opsModelStore(c)
	if !ok {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	current, err := store.AIProvider(c.Request.Context(), id)
	if errors.Is(err, opsconfig.ErrModelRouteNotFound) {
		writeError(c, http.StatusNotFound, "not_found", "模型供应商不存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var input opsProviderInput
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "模型供应商参数不正确", nil)
		return
	}
	if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" && s.cfg.AppEnv == "prod" && !requestUsesHTTPS(c.Request, s.cfg.TrustedProxies) {
		writeError(c, http.StatusUpgradeRequired, "https_required", "生产环境只能通过 HTTPS 保存 API Key", nil)
		return
	}
	provider, err := s.normalizeProviderInput(input, current)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "ai_provider_invalid", err.Error(), nil)
		return
	}
	rawCapabilities, _ := json.Marshal(provider.Capabilities)
	tag, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
		UPDATE ai_provider
		   SET name=$2,base_url=$3,api_key=$4,timeout_seconds=$5,max_retries=$6,
		       capabilities=$7,enabled=$8,revision=revision+1,updated_at=now()
		 WHERE id=$1::uuid
	`, id, provider.Name, provider.BaseURL, provider.APIKey, provider.TimeoutSeconds,
		provider.MaxRetries, rawCapabilities, provider.Enabled)
	if uniqueViolation(err) {
		writeError(c, http.StatusConflict, "ai_provider_exists", "模型供应商名称已存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(c, http.StatusNotFound, "not_found", "模型供应商不存在", nil)
		return
	}
	fields := providerChangedFields(input)
	if err := s.appendOpsAudit(c.Request.Context(), c, "ai_provider.updated", "ai_provider", id, gin.H{"fields": fields, "apiKeyChanged": input.APIKey != nil}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteOpsAIProvider(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var routes int
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT count(*) FROM ai_model_route WHERE provider_id=$1::uuid`, id).Scan(&routes); err != nil {
		writeServiceError(c, err)
		return
	}
	if routes > 0 {
		writeError(c, http.StatusConflict, "ai_provider_in_use", "请先删除或改绑使用该供应商的业务路由", gin.H{"routes": routes})
		return
	}
	tag, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `DELETE FROM ai_provider WHERE id=$1::uuid`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(c, http.StatusNotFound, "not_found", "模型供应商不存在", nil)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "ai_provider.deleted", "ai_provider", id, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) clearOpsAIProviderKey(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	tag, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
		UPDATE ai_provider SET api_key='',enabled=false,revision=revision+1,updated_at=now() WHERE id=$1::uuid
	`, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(c, http.StatusNotFound, "not_found", "模型供应商不存在", nil)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "ai_provider.key_cleared", "ai_provider", id, nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) opsModelRoutes(c *gin.Context) {
	store, ok := s.opsModelStore(c)
	if !ok {
		return
	}
	routes, err := store.ModelRoutes(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": routes, "purposes": opsconfig.ModelPurposes})
}

func (s *Server) updateOpsModelRoute(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	purpose, err := opsconfig.NormalizeModelPurpose(c.Param("purpose"))
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", err.Error(), nil)
		return
	}
	var input struct {
		ProviderID string         `json:"providerId"`
		Model      string         `json:"model"`
		Parameters map[string]any `json:"parameters"`
	}
	if c.ShouldBindJSON(&input) != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "模型路由参数不正确", nil)
		return
	}
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model, err = opsconfig.NormalizeAIModel(input.Model)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", err.Error(), nil)
		return
	}
	store := opsconfig.New(s.deps.Pools.Ops, 0)
	provider, err := store.AIProvider(c.Request.Context(), input.ProviderID)
	if errors.Is(err, opsconfig.ErrModelRouteNotFound) {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", "所选模型供应商不存在", nil)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if !provider.Enabled || !provider.APIKeySet {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", "所选模型供应商未启用或尚未配置 API Key", nil)
		return
	}
	if purpose.RequiresVision() && !provider.Capabilities.Vision {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", "该槽位需要供应商声明视觉能力", nil)
		return
	}
	if input.Parameters == nil {
		input.Parameters = map[string]any{}
	}
	input.Parameters, err = opsconfig.NormalizeModelParameters(input.Parameters)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", err.Error(), nil)
		return
	}
	rawParameters, _ := json.Marshal(input.Parameters)
	_, err = s.deps.Pools.Ops.Exec(c.Request.Context(), `
		INSERT INTO ai_model_route (purpose,provider_id,model,parameters)
		VALUES ($1,$2::uuid,$3,$4)
		ON CONFLICT (purpose) DO UPDATE
		SET provider_id=EXCLUDED.provider_id,model=EXCLUDED.model,parameters=EXCLUDED.parameters,
		    revision=ai_model_route.revision+1,updated_at=now()
	`, purpose, input.ProviderID, input.Model, rawParameters)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "model_route.updated", "model_route", string(purpose), gin.H{"providerId": input.ProviderID, "model": input.Model}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteOpsModelRoute(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	purpose, err := opsconfig.NormalizeModelPurpose(c.Param("purpose"))
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", err.Error(), nil)
		return
	}
	tag, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `DELETE FROM ai_model_route WHERE purpose=$1`, purpose)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(c, http.StatusNotFound, "not_found", "模型路由不存在", nil)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "model_route.deleted", "model_route", string(purpose), nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) testOpsModelRoute(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	purpose, err := opsconfig.NormalizeModelPurpose(c.Param("purpose"))
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "model_route_invalid", err.Error(), nil)
		return
	}
	binding, err := s.runtimeModelBinding(c.Request.Context(), purpose)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "model_route_incomplete", err.Error(), nil)
		return
	}
	client, err := llm.NewOpenAICompatible(llm.Config{
		BaseURL: binding.BaseURL, APIKey: binding.APIKey, Timeout: binding.Timeout, MaxRetries: binding.MaxRetries,
		AllowPrivateNetwork: s.cfg.AllowPrivateAINetwork(),
	})
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "model_route_incomplete", "无法初始化模型连接", nil)
		return
	}
	request := syntheticRouteRequest(purpose, binding.Model)
	binding.ApplyParameters(&request)
	response, err := client.Call(c.Request.Context(), request)
	if err != nil {
		status := 0
		var callErr *llm.CallError
		if errors.As(err, &callErr) {
			status = callErr.StatusCode
		}
		reason := modelTestReason(err)
		_ = s.appendOpsAudit(c.Request.Context(), c, "model_route.test_failed", "model_route", string(purpose), gin.H{"providerId": binding.ProviderID, "statusCode": status, "reason": reason})
		writeError(c, http.StatusBadGateway, "model_route_test_failed", "模型路由连接或能力测试失败", gin.H{"statusCode": status, "reason": reason})
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "model_route.test_succeeded", "model_route", string(purpose), gin.H{"providerId": binding.ProviderID, "model": response.Model, "durationMs": response.DurationMS}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "purpose": purpose, "providerId": binding.ProviderID, "provider": binding.ProviderName, "model": response.Model, "durationMs": response.DurationMS})
}

// credentialInError 兜底擦掉万一被供应商回包带出来的密钥。key 只走 Authorization
// 头，正常不会出现在错误里，但这条字符串是要交给界面的，不赌供应商的实现。
var credentialInError = regexp.MustCompile(`(?i)bearer\s+\S+|sk-[A-Za-z0-9_\-]{8,}`)

// modelTestReason 把一次模型连通性测试的失败原因压成一句能给 ops 看的诊断。
//
// 这两个测试端点以前只回一句「连接失败」，真正的原因全被吞掉：DNS 解析不到、
// 地址被 llm 的 SSRF 守卫拦下、鉴权失败、模型 ID 不存在，界面上长得一模一样，
// 只有翻 worker 日志才分得清。ops 是平台最高权限角色，这点信息该给他。
func modelTestReason(err error) string {
	if err == nil {
		return ""
	}
	reason := strings.Join(strings.Fields(credentialInError.ReplaceAllString(err.Error(), "***")), " ")
	if runes := []rune(reason); len(runes) > 400 {
		reason = string(runes[:400]) + "…"
	}
	return reason
}

func syntheticRouteRequest(purpose opsconfig.ModelPurpose, model string) llm.Request {
	request := llm.Request{
		Purpose: string(purpose), Model: model,
		System: "This is a synthetic connectivity check. Do not infer or retain user data.",
		Prompt: "Return one JSON object with an ok boolean set to true.", JSON: true, MaxTokens: 64,
	}
	if purpose.RequiresVision() {
		request.Image = &llm.Image{MediaType: "image/png", Data: syntheticVisionPNG()}
	}
	return request
}

// Several OpenAI-compatible vision gateways reject 1×1 probe images before
// the model sees them. Use a deterministic, ordinary-sized PNG so the check
// measures the configured model's image capability instead of vendor-specific
// minimum-dimension validation.
func syntheticVisionPNG() []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			shade := uint8(236)
			if (x/16+y/16)%2 == 0 {
				shade = 252
			}
			canvas.SetRGBA(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	for i := 20; i < 108; i++ {
		canvas.SetRGBA(i, i, color.RGBA{R: 225, G: 42, B: 69, A: 255})
		canvas.SetRGBA(127-i, i, color.RGBA{R: 38, G: 98, B: 201, A: 255})
	}
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, canvas)
	return encoded.Bytes()
}

func (s *Server) normalizeProviderInput(input opsProviderInput, current opsconfig.AIProvider) (opsconfig.AIProvider, error) {
	provider := current
	if provider.TimeoutSeconds == 0 {
		provider.TimeoutSeconds = 90
	}
	if provider.AuthType == "" {
		provider.AuthType = "bearer"
	}
	if current.ID == "" {
		provider.Capabilities = opsconfig.ProviderCapabilities{JSON: true, Stream: true}
	}
	if input.Name != nil {
		provider.Name = strings.TrimSpace(*input.Name)
	}
	if provider.Name == "" || len([]rune(provider.Name)) > 80 || hasControlExceptWhitespace(provider.Name) {
		return opsconfig.AIProvider{}, errors.New("供应商名称必须是 1—80 个可见字符")
	}
	if input.BaseURL != nil {
		baseURL, err := opsconfig.NormalizeAIBaseURL(*input.BaseURL, s.cfg.AllowPrivateAINetwork())
		if err != nil {
			return opsconfig.AIProvider{}, err
		}
		provider.BaseURL = baseURL
	}
	if provider.BaseURL == "" {
		return opsconfig.AIProvider{}, errors.New("模型 API URL 未配置")
	}
	if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" {
		if s.cfg.AppEnv == "prod" && s.deps.AICipher == nil {
			return opsconfig.AIProvider{}, errors.New("AI_CONFIG_SECRET_KEY 未配置，无法安全保存 API Key")
		}
		key, err := opsconfig.NormalizeAIAPIKey(*input.APIKey)
		if err != nil {
			return opsconfig.AIProvider{}, err
		}
		if s.deps.AICipher == nil {
			return opsconfig.AIProvider{}, errors.New("服务端未配置 AI_CONFIG_SECRET_KEY，无法安全保存 API Key")
		}
		provider.APIKey, err = s.deps.AICipher.Seal(key)
		if err != nil {
			return opsconfig.AIProvider{}, err
		}
	}
	if input.TimeoutSeconds != nil {
		provider.TimeoutSeconds = *input.TimeoutSeconds
	}
	if provider.TimeoutSeconds < 5 || provider.TimeoutSeconds > 600 {
		return opsconfig.AIProvider{}, errors.New("请求超时必须是 5—600 秒")
	}
	if input.MaxRetries != nil {
		provider.MaxRetries = *input.MaxRetries
	}
	if provider.MaxRetries < 0 || provider.MaxRetries > 5 {
		return opsconfig.AIProvider{}, errors.New("自动重试必须是 0—5 次")
	}
	if input.Capabilities != nil {
		provider.Capabilities = *input.Capabilities
	}
	if input.Enabled != nil {
		provider.Enabled = *input.Enabled
	} else if current.ID == "" {
		provider.Enabled = true
	}
	if provider.Enabled && !opsconfig.IsSealed(provider.APIKey) {
		return opsconfig.AIProvider{}, errors.New("启用模型供应商前必须配置 API Key")
	}
	return provider, nil
}

func providerChangedFields(input opsProviderInput) []string {
	fields := make([]string, 0, 7)
	if input.Name != nil {
		fields = append(fields, "name")
	}
	if input.BaseURL != nil {
		fields = append(fields, "baseUrl")
	}
	if input.APIKey != nil {
		fields = append(fields, "apiKey")
	}
	if input.TimeoutSeconds != nil {
		fields = append(fields, "timeoutSeconds")
	}
	if input.MaxRetries != nil {
		fields = append(fields, "maxRetries")
	}
	if input.Capabilities != nil {
		fields = append(fields, "capabilities")
	}
	if input.Enabled != nil {
		fields = append(fields, "enabled")
	}
	sort.Strings(fields)
	return fields
}

func (s *Server) opsModelStore(c *gin.Context) (*opsconfig.Store, bool) {
	if !s.opsPool(c) {
		return nil, false
	}
	if store, ok := s.opsConfig.(*opsconfig.Store); ok {
		return store, true
	}
	return opsconfig.New(s.deps.Pools.Ops, 0), true
}
