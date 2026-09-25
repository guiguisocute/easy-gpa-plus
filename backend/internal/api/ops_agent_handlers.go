package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/opsconfig"
)

func (s *Server) opsAgent(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	stored, err := s.opsConfig.AI(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	modelStore := opsconfig.New(s.deps.Pools.Ops, 0)
	providers, err := modelStore.AIProviders(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	routes, err := modelStore.ModelRoutes(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	providerKeySet := false
	for _, provider := range providers {
		providerKeySet = providerKeySet || provider.APIKeySet
	}
	fallback := s.aiFallback()
	public, sources := publicAIConfig(stored, fallback)
	runtime, resolveErr := opsconfig.ResolveAI(stored, s.deps.AICipher, fallback, s.cfg.AllowPrivateAINetwork())
	legacyReady := resolveErr == nil
	if legacyReady {
		public = gin.H{"baseUrl": runtime.BaseURL, "textModel": runtime.TextModel, "visionModel": runtime.VisionModel, "agentModel": runtime.AgentModel}
		sources = runtime.Sources
	}
	materialErr := s.runtimeModelBindingsReady(c.Request.Context(), opsconfig.PurposeMaterialVision, opsconfig.PurposeMaterialCompose)
	agentErr := s.runtimeModelBindingsReady(c.Request.Context(), opsconfig.PurposeAgentText)
	knowledgeErr := s.runtimeModelBindingsReady(c.Request.Context(), opsconfig.PurposeKnowledgeOCR, opsconfig.PurposeAgentText)
	ready := materialErr == nil
	routeStatus := make(map[string]gin.H, len(opsconfig.ModelPurposes))
	for _, purpose := range opsconfig.ModelPurposes {
		binding, bindingErr := s.runtimeModelBinding(c.Request.Context(), purpose)
		item := gin.H{"ready": bindingErr == nil}
		if bindingErr == nil {
			item["providerId"] = binding.ProviderID
			item["provider"] = binding.ProviderName
			item["model"] = binding.Model
			item["legacy"] = binding.ProviderID == ""
		} else {
			item["reason"] = bindingErr.Error()
		}
		routeStatus[string(purpose)] = item
	}
	flags := s.runtimeFlags(c.Request.Context())
	var dailyMessages, dailyAttachments, dailyAttachmentBytes int64
	if err := s.deps.Pools.Ops.QueryRow(c.Request.Context(), `SELECT message_count,attachment_count,attachment_bytes FROM ops_agent_daily_usage()`).Scan(&dailyMessages, &dailyAttachments, &dailyAttachmentBytes); err != nil {
		writeServiceError(c, err)
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "agent.read", "ai_config", "ai", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	reason := ""
	if materialErr != nil {
		reason = materialErr.Error()
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"config": public, "sources": sources,
		"enabled": flags.AIEnabled, "ready": ready, "statusReason": reason,
		"legacyReady": legacyReady, "agentReady": agentErr == nil,
		"routeStatus": routeStatus, "providerCount": len(providers), "routeCount": len(routes),
		"knowledgeEnabled":       flags.KnowledgeEnabled,
		"agentActionsEnabled":    flags.AgentActionsEnabled,
		"knowledgeEgressEnabled": flags.KnowledgeEgressEnabled,
		"knowledgeReady":         knowledgeErr == nil && flags.AIEnabled && flags.KnowledgeEnabled && flags.KnowledgeEgressEnabled,
		"knowledgeStatusReason":  knowledgeStatusReason(knowledgeErr == nil, errorText(knowledgeErr), flags),
		"limits": gin.H{
			"agentMaxSteps":                 stored.AgentMaxSteps,
			"agentTimeoutSeconds":           stored.AgentTimeoutSeconds,
			"agentMaxAnswerKb":              stored.AgentMaxAnswerKB,
			"agentToolResultKb":             stored.AgentToolResultKB,
			"agentToolScanMb":               stored.AgentToolScanMB,
			"agentDailyMessages":            stored.AgentDailyMessages,
			"materialDailyBatches":          stored.MaterialDailyBatches,
			"materialActiveBatches":         stored.MaterialActiveBatches,
			"knowledgeMaxFilesPerClass":     stored.KnowledgeMaxFilesPerClass,
			"knowledgeMaxStorageMbPerClass": stored.KnowledgeMaxStorageMBPerClass,
			"materialMaxItems":              stored.MaterialMaxItems,
			"materialMaxFileMb":             stored.MaterialMaxFileMB,
			"materialMaxBatchMb":            stored.MaterialMaxBatchMB,
			"materialMaxPdfPages":           stored.MaterialMaxPDFPages,
			"materialConcurrency":           stored.MaterialConcurrency,
			"materialRetentionDays":         stored.MaterialRetentionDays,
			"materialAllowedFormats":        stored.MaterialAllowedFormats,
			"agentAttachmentMaxFileMb":      stored.AgentAttachmentMaxFileMB,
			"agentAttachmentMaxMessageMb":   stored.AgentAttachmentMaxMessageMB,
			"agentAttachmentDailyMb":        stored.AgentAttachmentDailyMB,
			"agentAttachmentMaxCount":       stored.AgentAttachmentMaxCount,
		},
		"usage":           gin.H{"dailyMessages": dailyMessages, "dailyAttachments": dailyAttachments, "dailyAttachmentBytes": dailyAttachmentBytes},
		"apiKeySet":       providerKeySet || strings.TrimSpace(stored.APIKey) != "" || strings.TrimSpace(fallback.APIKey) != "",
		"storedApiKeySet": strings.TrimSpace(stored.APIKey) != "",
		"secretKeyReady":  s.deps.AICipher != nil,
	})
}

func publicAIConfig(stored, fallback opsconfig.AI) (gin.H, opsconfig.AIFieldSources) {
	choose := func(primary, secondary string) (string, string) {
		if value := strings.TrimSpace(primary); value != "" {
			return value, opsconfig.AISourceDatabase
		}
		if value := strings.TrimSpace(secondary); value != "" {
			return value, opsconfig.AISourceEnvironment
		}
		return "", opsconfig.AISourceNone
	}
	baseURL, baseSource := choose(stored.BaseURL, fallback.BaseURL)
	textModel, textSource := choose(stored.TextModel, fallback.TextModel)
	visionModel, visionSource := choose(stored.VisionModel, fallback.VisionModel)
	agentModel, agentSource := choose(stored.AgentModel, fallback.AgentModel)
	if agentModel == "" {
		agentModel, agentSource = textModel, opsconfig.AISourceTextModel
	}
	keySource := opsconfig.AISourceNone
	if strings.TrimSpace(stored.APIKey) != "" {
		keySource = opsconfig.AISourceDatabase
	} else if strings.TrimSpace(fallback.APIKey) != "" {
		keySource = opsconfig.AISourceEnvironment
	}
	return gin.H{"baseUrl": baseURL, "textModel": textModel, "visionModel": visionModel, "agentModel": agentModel}, opsconfig.AIFieldSources{
		BaseURL: baseSource, APIKey: keySource, TextModel: textSource, VisionModel: visionSource, AgentModel: agentSource,
	}
}

func knowledgeStatusReason(ready bool, providerReason string, flags opsconfig.Flags) string {
	switch {
	case !ready:
		return providerReason
	case !flags.AIEnabled:
		return "AI 总开关未开启"
	case !flags.KnowledgeEnabled:
		return "班级知识库与问答未开启"
	case !flags.KnowledgeEgressEnabled:
		return "知识内容外发未开启"
	default:
		return ""
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) updateOpsAgent(c *gin.Context) {
	if !s.opsPool(c) {
		return
	}
	var input map[string]any
	if err := c.ShouldBindJSON(&input); err != nil || len(input) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "Agent 配置参数不正确", nil)
		return
	}
	allowed := map[string]bool{
		"baseUrl": true, "apiKey": true, "textModel": true, "visionModel": true, "agentModel": true,
		"agentMaxSteps": true, "agentTimeoutSeconds": true, "agentMaxAnswerKb": true, "agentToolResultKb": true, "agentToolScanMb": true,
		"agentDailyMessages": true, "materialDailyBatches": true, "materialActiveBatches": true, "knowledgeMaxFilesPerClass": true, "knowledgeMaxStorageMbPerClass": true,
		"materialMaxItems": true, "materialMaxFileMb": true, "materialMaxPdfPages": true,
		"materialMaxBatchMb":  true,
		"materialConcurrency": true, "materialRetentionDays": true, "materialAllowedFormats": true,
		"agentAttachmentMaxFileMb": true, "agentAttachmentMaxMessageMb": true,
		"agentAttachmentDailyMb": true, "agentAttachmentMaxCount": true,
	}
	for key := range input {
		if !allowed[key] {
			writeError(c, http.StatusUnprocessableEntity, "ai_config_invalid", "Agent 配置包含不允许的字段", gin.H{"field": key})
			return
		}
	}
	if _, includesKey := input["apiKey"]; includesKey && s.cfg.AppEnv == "prod" && !requestUsesHTTPS(c.Request, s.cfg.TrustedProxies) {
		writeError(c, http.StatusUpgradeRequired, "https_required", "生产环境只能通过 HTTPS 保存 API Key", nil)
		return
	}
	if err := normalizeOpsAgentInput(input, s.deps.AICipher, s.cfg.AllowPrivateAINetwork()); err != nil {
		writeError(c, http.StatusUnprocessableEntity, "ai_config_invalid", err.Error(), nil)
		return
	}
	current, err := s.opsConfig.AI(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	fileMB := inputInt(input, "agentAttachmentMaxFileMb", current.AgentAttachmentMaxFileMB)
	messageMB := inputInt(input, "agentAttachmentMaxMessageMb", current.AgentAttachmentMaxMessageMB)
	dailyMB := inputInt(input, "agentAttachmentDailyMb", current.AgentAttachmentDailyMB)
	if messageMB < fileMB || dailyMB < messageMB {
		writeError(c, http.StatusUnprocessableEntity, "ai_config_invalid", "Agent 每消息附件上限不得小于单文件上限，每日上限不得小于每消息上限", nil)
		return
	}
	raw, _ := json.Marshal(input)
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
		INSERT INTO ops_config (key,value) VALUES ('ai',$1)
		ON CONFLICT (key) DO UPDATE SET value=ops_config.value||EXCLUDED.value,updated_at=now()
	`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("ai")
	fields := make([]string, 0, len(input))
	for key := range input {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	if err := s.appendOpsAudit(c.Request.Context(), c, "agent.updated", "ai_config", "ai", gin.H{
		"fields": fields, "apiKeyChanged": input["apiKey"] != nil,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

func inputInt(input map[string]any, key string, fallback int) int {
	if value, ok := input[key].(int); ok {
		return value
	}
	return fallback
}

func requestUsesHTTPS(request *http.Request, trustedProxies []string) bool {
	if request != nil && request.TLS != nil {
		return true
	}
	if request == nil {
		return false
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	remote := net.ParseIP(strings.TrimSpace(host))
	trusted := false
	for _, proxy := range trustedProxies {
		if candidate := net.ParseIP(proxy); candidate != nil {
			trusted = candidate.Equal(remote)
		} else if _, network, parseErr := net.ParseCIDR(proxy); parseErr == nil {
			trusted = network.Contains(remote)
		}
		if trusted {
			break
		}
	}
	if !trusted {
		return false
	}
	forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwarded, "https")
}

func normalizeOpsAgentInput(input map[string]any, cipher *opsconfig.Cipher, allowLoopbackHTTP bool) error {
	if value, exists := input["baseUrl"]; exists {
		text, ok := value.(string)
		if !ok {
			return errors.New("模型 API URL 必须是字符串")
		}
		text = strings.TrimSpace(text)
		if text != "" {
			normalized, err := opsconfig.NormalizeAIBaseURL(text, allowLoopbackHTTP)
			if err != nil {
				return err
			}
			text = normalized
		}
		input["baseUrl"] = text
	}
	for _, key := range []string{"textModel", "visionModel", "agentModel"} {
		value, exists := input[key]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return errors.New("模型 ID 必须是字符串")
		}
		text = strings.TrimSpace(text)
		if text != "" {
			normalized, err := opsconfig.NormalizeAIModel(text)
			if err != nil {
				return err
			}
			text = normalized
		}
		input[key] = text
	}
	limits := map[string][2]int{
		"agentMaxSteps": {1, 64}, "agentTimeoutSeconds": {5, 600},
		"agentMaxAnswerKb": {16, 512}, "agentToolResultKb": {1, 256}, "agentToolScanMb": {1, 256}, "agentDailyMessages": {1, 10000}, "materialDailyBatches": {1, 100}, "materialActiveBatches": {1, 20},
		"knowledgeMaxFilesPerClass": {1, 10000}, "knowledgeMaxStorageMbPerClass": {1, 102400},
		"materialMaxItems": {1, 100}, "materialMaxFileMb": {1, 50}, "materialMaxBatchMb": {1, 2048}, "materialMaxPdfPages": {1, 64},
		"materialConcurrency": {1, 8}, "materialRetentionDays": {1, 30},
		"agentAttachmentMaxFileMb": {1, 50}, "agentAttachmentMaxMessageMb": {1, 100},
		"agentAttachmentDailyMb": {1, 10000}, "agentAttachmentMaxCount": {1, 12},
	}
	for key, bounds := range limits {
		value, exists := input[key]
		if !exists {
			continue
		}
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) || int(number) < bounds[0] || int(number) > bounds[1] {
			return fmt.Errorf("%s 必须是 %d—%d 的整数", key, bounds[0], bounds[1])
		}
		input[key] = int(number)
	}
	if value, exists := input["materialAllowedFormats"]; exists {
		formats := make([]string, 0)
		switch values := value.(type) {
		case []any:
			for _, item := range values {
				text, ok := item.(string)
				if !ok {
					return errors.New("允许的材料格式必须是字符串列表")
				}
				formats = append(formats, text)
			}
		case []string:
			formats = append(formats, values...)
		default:
			return errors.New("允许的材料格式必须是字符串列表")
		}
		normalized, err := opsconfig.NormalizeMaterialAllowedFormats(formats)
		if err != nil {
			return err
		}
		input["materialAllowedFormats"] = normalized
	}
	value, exists := input["apiKey"]
	if !exists {
		return nil
	}
	plain, ok := value.(string)
	if !ok {
		return errors.New("AI API Key 必须是字符串")
	}
	normalized, err := opsconfig.NormalizeAIAPIKey(plain)
	if err != nil {
		return err
	}
	if cipher == nil {
		return errors.New("服务端未配置 AI_CONFIG_SECRET_KEY，无法安全保存 API Key")
	}
	sealed, err := cipher.Seal(normalized)
	if err != nil {
		return err
	}
	input["apiKey"] = sealed
	return nil
}

func (s *Server) clearOpsAgentKey(c *gin.Context) {
	s.clearOpsAgentFields(c, `{"apiKey":""}`, "agent.key_cleared")
}

func (s *Server) clearOpsAgent(c *gin.Context) {
	s.clearOpsAgentFields(c, `{"baseUrl":"","apiKey":"","textModel":"","visionModel":"","agentModel":"","agentMaxSteps":24,"agentTimeoutSeconds":180,"agentMaxAnswerKb":128,"agentToolResultKb":32,"agentToolScanMb":64,"agentDailyMessages":50,"materialDailyBatches":5,"materialActiveBatches":3,"knowledgeMaxFilesPerClass":1000,"knowledgeMaxStorageMbPerClass":1024,"materialMaxItems":100,"materialMaxFileMb":50,"materialMaxBatchMb":256,"materialMaxPdfPages":64,"materialConcurrency":2,"materialRetentionDays":7,"materialAllowedFormats":["jpeg","png","webp","pdf"],"agentAttachmentMaxFileMb":5,"agentAttachmentMaxMessageMb":12,"agentAttachmentDailyMb":100,"agentAttachmentMaxCount":4}`, "agent.config_cleared")
}

func (s *Server) clearOpsAgentFields(c *gin.Context, raw, action string) {
	if !s.opsPool(c) {
		return
	}
	if _, err := s.deps.Pools.Ops.Exec(c.Request.Context(), `
		INSERT INTO ops_config (key,value) VALUES ('ai',$1::jsonb)
		ON CONFLICT (key) DO UPDATE SET value=ops_config.value||EXCLUDED.value,updated_at=now()
	`, raw); err != nil {
		writeServiceError(c, err)
		return
	}
	s.opsConfig.Invalidate("ai")
	if err := s.appendOpsAudit(c.Request.Context(), c, action, "ai_config", "ai", nil); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

func (s *Server) testOpsAgent(c *gin.Context) {
	var input struct {
		Slot string `json:"slot"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "连接测试参数不正确", nil)
		return
	}
	input.Slot = strings.ToLower(strings.TrimSpace(input.Slot))
	if input.Slot != "text" && input.Slot != "vision" && input.Slot != "agent" {
		writeError(c, http.StatusUnprocessableEntity, "slot_invalid", "测试槽位只能是 text、vision 或 agent", nil)
		return
	}
	purpose := opsconfig.PurposeMaterialCompose
	if input.Slot == "vision" {
		purpose = opsconfig.PurposeMaterialVision
	} else if input.Slot == "agent" {
		purpose = opsconfig.PurposeAgentText
	}
	binding, err := s.runtimeModelBinding(c.Request.Context(), purpose)
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "ai_config_incomplete", err.Error(), nil)
		return
	}
	client, err := llm.NewOpenAICompatible(llm.Config{BaseURL: binding.BaseURL, APIKey: binding.APIKey, Timeout: binding.Timeout, MaxRetries: binding.MaxRetries, AllowPrivateNetwork: s.cfg.AllowPrivateAINetwork()})
	if err != nil {
		writeError(c, http.StatusUnprocessableEntity, "ai_config_incomplete", "无法初始化模型连接", nil)
		return
	}
	req := syntheticRouteRequest(purpose, binding.Model)
	if input.Slot == "agent" {
		req.System = "This is a synthetic knowledge-agent connectivity check. Never request tools or user data."
	}
	response, err := client.Call(c.Request.Context(), req)
	if err != nil {
		status := 0
		var callErr *llm.CallError
		if errors.As(err, &callErr) {
			status = callErr.StatusCode
		}
		reason := modelTestReason(err)
		_ = s.appendOpsAudit(c.Request.Context(), c, "agent.test_failed", "ai_config", input.Slot, gin.H{"statusCode": status, "reason": reason})
		message := "模型接口连接失败"
		if status > 0 {
			message += "（HTTP " + http.StatusText(status) + "）"
		}
		writeError(c, http.StatusBadGateway, "ai_test_failed", message, gin.H{"statusCode": status, "reason": reason})
		return
	}
	if err := s.appendOpsAudit(c.Request.Context(), c, "agent.test_succeeded", "ai_config", input.Slot, gin.H{
		"model": response.Model, "durationMs": response.DurationMS,
	}); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"ok": true, "slot": input.Slot, "model": response.Model, "durationMs": response.DurationMS})
}
