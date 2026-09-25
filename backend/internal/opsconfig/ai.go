package opsconfig

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
)

const (
	AISourceDatabase    = "database"
	AISourceEnvironment = "environment"
	AISourceTextModel   = "textModel"
	AISourceNone        = "none"
)

var materialAllowedFormatOrder = []string{"jpeg", "png", "webp", "pdf"}

func DefaultMaterialAllowedFormats() []string {
	return append([]string(nil), materialAllowedFormatOrder...)
}

// NormalizeMaterialAllowedFormats keeps the persisted/API contract small and
// deterministic. Extensions and MIME aliases are resolved at the upload gate;
// operational settings use these four stable family names only.
func NormalizeMaterialAllowedFormats(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("允许的材料格式至少保留一种")
	}
	selected := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "jpg" {
			value = "jpeg"
		}
		switch value {
		case "jpeg", "png", "webp", "pdf":
			selected[value] = true
		default:
			return nil, fmt.Errorf("不支持的材料格式：%s", raw)
		}
	}
	result := make([]string, 0, len(selected))
	for _, value := range materialAllowedFormatOrder {
		if selected[value] {
			result = append(result, value)
		}
	}
	return result, nil
}

type AIFieldSources struct {
	BaseURL     string `json:"baseUrl"`
	APIKey      string `json:"apiKey"`
	TextModel   string `json:"textModel"`
	VisionModel string `json:"visionModel"`
	AgentModel  string `json:"agentModel"`
}

// AIRuntime contains the effective, validated settings used by a worker. It is
// deliberately a server-only type because APIKey is plaintext in memory.
type AIRuntime struct {
	AI
	Sources AIFieldSources
}

// ResolveAI overlays encrypted database settings on deployment fallbacks,
// decrypts the provider key and validates the final transport contract.
func ResolveAI(stored AI, cipher *Cipher, fallback AI, allowLoopbackHTTP bool) (AIRuntime, error) {
	runtime := AIRuntime{}
	runtime.BaseURL, runtime.Sources.BaseURL = choose(stored.BaseURL, fallback.BaseURL)
	runtime.TextModel, runtime.Sources.TextModel = choose(stored.TextModel, fallback.TextModel)
	runtime.VisionModel, runtime.Sources.VisionModel = choose(stored.VisionModel, fallback.VisionModel)
	runtime.AgentModel, runtime.Sources.AgentModel = choose(stored.AgentModel, fallback.AgentModel)
	if strings.TrimSpace(runtime.AgentModel) == "" {
		runtime.AgentModel = runtime.TextModel
		runtime.Sources.AgentModel = AISourceTextModel
	}
	runtime.AgentMaxSteps = stored.AgentMaxSteps
	runtime.AgentTimeoutSeconds = stored.AgentTimeoutSeconds
	runtime.AgentMaxAnswerKB = stored.AgentMaxAnswerKB
	runtime.AgentToolResultKB = stored.AgentToolResultKB
	runtime.AgentToolScanMB = stored.AgentToolScanMB
	runtime.AgentDailyMessages = stored.AgentDailyMessages
	runtime.MaterialDailyBatches = stored.MaterialDailyBatches
	runtime.MaterialActiveBatches = stored.MaterialActiveBatches
	runtime.KnowledgeMaxFilesPerClass = stored.KnowledgeMaxFilesPerClass
	runtime.KnowledgeMaxStorageMBPerClass = stored.KnowledgeMaxStorageMBPerClass
	runtime.MaterialMaxItems = stored.MaterialMaxItems
	runtime.MaterialMaxFileMB = stored.MaterialMaxFileMB
	runtime.MaterialMaxBatchMB = stored.MaterialMaxBatchMB
	runtime.MaterialMaxPDFPages = stored.MaterialMaxPDFPages
	runtime.MaterialConcurrency = stored.MaterialConcurrency
	runtime.MaterialRetentionDays = stored.MaterialRetentionDays
	runtime.MaterialAllowedFormats = append([]string(nil), stored.MaterialAllowedFormats...)
	runtime.AgentAttachmentMaxFileMB = stored.AgentAttachmentMaxFileMB
	runtime.AgentAttachmentMaxMessageMB = stored.AgentAttachmentMaxMessageMB
	runtime.AgentAttachmentDailyMB = stored.AgentAttachmentDailyMB
	runtime.AgentAttachmentMaxCount = stored.AgentAttachmentMaxCount

	if encrypted := strings.TrimSpace(stored.APIKey); encrypted != "" {
		if !IsSealed(encrypted) {
			return AIRuntime{}, errors.New("数据库中的 AI API Key 不是受支持的密文格式")
		}
		if cipher == nil {
			return AIRuntime{}, errors.New("AI_CONFIG_SECRET_KEY 未配置，无法解密数据库中的 AI API Key")
		}
		plain, err := cipher.Open(encrypted)
		if err != nil {
			return AIRuntime{}, err
		}
		runtime.APIKey = plain
		runtime.Sources.APIKey = AISourceDatabase
	} else {
		runtime.APIKey = strings.TrimSpace(fallback.APIKey)
		if runtime.APIKey == "" {
			runtime.Sources.APIKey = AISourceNone
		} else {
			runtime.Sources.APIKey = AISourceEnvironment
		}
	}

	var err error
	if runtime.BaseURL, err = NormalizeAIBaseURL(runtime.BaseURL, allowLoopbackHTTP); err != nil {
		return AIRuntime{}, err
	}
	if runtime.TextModel, err = NormalizeAIModel(runtime.TextModel); err != nil {
		return AIRuntime{}, fmt.Errorf("文字模型：%w", err)
	}
	if runtime.VisionModel, err = NormalizeAIModel(runtime.VisionModel); err != nil {
		return AIRuntime{}, fmt.Errorf("识图模型：%w", err)
	}
	if runtime.AgentModel, err = NormalizeAIModel(runtime.AgentModel); err != nil {
		return AIRuntime{}, fmt.Errorf("Agent 模型：%w", err)
	}
	if runtime.APIKey, err = NormalizeAIAPIKey(runtime.APIKey); err != nil {
		return AIRuntime{}, err
	}
	return runtime, nil
}

func choose(primary, fallback string) (string, string) {
	if value := strings.TrimSpace(primary); value != "" {
		return value, AISourceDatabase
	}
	if value := strings.TrimSpace(fallback); value != "" {
		return value, AISourceEnvironment
	}
	return "", AISourceNone
}

// NormalizeAIBaseURL requires encrypted transport. Plain HTTP is accepted only
// for loopback development endpoints, where the secret never leaves the host.
func NormalizeAIBaseURL(raw string, allowLoopbackHTTP bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("模型 API URL 未配置")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", errors.New("模型 API URL 格式不正确")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("模型 API URL 不允许包含账号、查询参数或片段")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowLoopbackHTTP || !isLoopbackHost(parsed.Hostname()) {
			return "", errors.New("模型 API URL 必须使用 HTTPS")
		}
	default:
		return "", errors.New("模型 API URL 必须使用 HTTPS")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func NormalizeAIModel(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("模型 ID 未配置")
	}
	if len(value) > 128 {
		return "", errors.New("模型 ID 不能超过 128 个字符")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("模型 ID 不能包含空白或控制字符")
		}
	}
	return value, nil
}

func NormalizeAIAPIKey(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) < 8 || len(value) > 4096 {
		return "", errors.New("AI API Key 未配置或长度不正确")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("AI API Key 不能包含空白或控制字符")
		}
	}
	return value, nil
}
