package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"easygpa/backend/internal/opsconfig"
)

func TestPublicAIConfigNeverSerializesProviderKey(t *testing.T) {
	public, sources := publicAIConfig(
		opsconfig.AI{BaseURL: "https://models.example/v1", APIKey: "enc:v1:database-ciphertext", TextModel: "text-model"},
		opsconfig.AI{APIKey: "environment-secret", VisionModel: "vision-model"},
	)
	raw, err := json.Marshal(map[string]any{"config": public, "sources": sources})
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	if strings.Contains(serialized, "database-ciphertext") || strings.Contains(serialized, "environment-secret") {
		t.Fatalf("public Agent response exposed key material: %s", serialized)
	}
}

func TestNormalizeOpsAgentInputEncryptsAPIKey(t *testing.T) {
	cipher, err := opsconfig.NewNamedCipher(strings.Repeat("z", 32), "AI_CONFIG_SECRET_KEY")
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"baseUrl": "https://models.example/v1/", "apiKey": "provider-secret-key",
		"textModel": "text-model", "visionModel": "vision-model",
	}
	if err := normalizeOpsAgentInput(input, cipher, false); err != nil {
		t.Fatal(err)
	}
	stored, _ := input["apiKey"].(string)
	if !opsconfig.IsSealed(stored) || strings.Contains(stored, "provider-secret") {
		t.Fatalf("API key was not sealed: %q", stored)
	}
	if input["baseUrl"] != "https://models.example/v1" {
		t.Fatalf("base URL = %v", input["baseUrl"])
	}
}

func TestRequestUsesHTTPSAcceptsTLSOrTrustedProxySignal(t *testing.T) {
	request := httptest.NewRequest("PUT", "http://example.test/api/v1/ops/agent", nil)
	if requestUsesHTTPS(request, []string{"192.0.2.1/32"}) {
		t.Fatal("plain request was treated as HTTPS")
	}
	request.Header.Set("X-Forwarded-Proto", "https")
	if !requestUsesHTTPS(request, []string{"192.0.2.1/32"}) {
		t.Fatal("reverse proxy HTTPS signal was ignored")
	}
	if requestUsesHTTPS(request, []string{"10.0.0.0/8"}) {
		t.Fatal("untrusted client spoofed the proxy HTTPS signal")
	}
	tlsRequest := httptest.NewRequest("PUT", "https://example.test/api/v1/ops/agent", nil)
	if !requestUsesHTTPS(tlsRequest, nil) {
		t.Fatal("TLS request was not recognized")
	}
}

func TestNormalizeOpsAgentInputRefusesKeyWithoutServerCipher(t *testing.T) {
	input := map[string]any{"apiKey": "provider-secret-key"}
	if err := normalizeOpsAgentInput(input, nil, false); err == nil {
		t.Fatal("API key was accepted without AI_CONFIG_SECRET_KEY")
	}
	if input["apiKey"] != "provider-secret-key" {
		t.Fatal("failed normalization unexpectedly mutated the key")
	}
}

func TestNormalizeOpsAgentInputAllowsEmptyPublicFieldsForEnvironmentFallback(t *testing.T) {
	input := map[string]any{"baseUrl": "", "textModel": "", "visionModel": ""}
	if err := normalizeOpsAgentInput(input, nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeOpsAgentInputValidatesMaterialLimits(t *testing.T) {
	valid := map[string]any{
		"materialMaxItems": float64(40), "materialMaxFileMb": float64(25),
		"materialMaxPdfPages": float64(32), "materialConcurrency": float64(4),
		"materialRetentionDays": float64(14), "materialActiveBatches": float64(4),
		"materialAllowedFormats": []any{"PDF", "jpg", "png"},
	}
	if err := normalizeOpsAgentInput(valid, nil, false); err != nil {
		t.Fatal(err)
	}
	if valid["materialConcurrency"] != 4 || valid["materialRetentionDays"] != 14 {
		t.Fatalf("normalized limits = %#v", valid)
	}
	formats, ok := valid["materialAllowedFormats"].([]string)
	if !ok || strings.Join(formats, ",") != "jpeg,png,pdf" {
		t.Fatalf("normalized formats = %#v", valid["materialAllowedFormats"])
	}
	invalid := map[string]any{"materialMaxFileMb": float64(51)}
	if err := normalizeOpsAgentInput(invalid, nil, false); err == nil {
		t.Fatal("oversized material file limit was accepted")
	}
	if err := normalizeOpsAgentInput(map[string]any{"materialAllowedFormats": []any{}}, nil, false); err == nil {
		t.Fatal("empty material format list was accepted")
	}
	if err := normalizeOpsAgentInput(map[string]any{"materialAllowedFormats": []any{"docx"}}, nil, false); err == nil {
		t.Fatal("unsupported material format was accepted")
	}
	if err := normalizeOpsAgentInput(map[string]any{"materialActiveBatches": float64(21)}, nil, false); err == nil {
		t.Fatal("unsafe active batch limit was accepted")
	}
	if err := normalizeOpsAgentInput(map[string]any{"agentToolScanMb": float64(257)}, nil, false); err == nil {
		t.Fatal("unsafe Agent tool scan budget was accepted")
	}
	if err := normalizeOpsAgentInput(map[string]any{"agentMaxAnswerKb": float64(513)}, nil, false); err == nil {
		t.Fatal("unsafe Agent answer limit was accepted")
	}
}
