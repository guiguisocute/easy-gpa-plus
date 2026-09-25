package opsconfig

import (
	"strings"
	"testing"
)

func TestResolveAIUsesEncryptedDatabaseKeyWithoutExposingCiphertext(t *testing.T) {
	cipher, err := NewNamedCipher(strings.Repeat("a", 32), "AI_CONFIG_SECRET_KEY")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal("provider-secret-key")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := ResolveAI(AI{
		BaseURL: "https://models.example/v1/", APIKey: sealed,
		TextModel: "text-v2", VisionModel: "vision-v2",
	}, cipher, AI{
		BaseURL: "https://fallback.example/v1", APIKey: "fallback-key",
		TextModel: "text-v1", VisionModel: "vision-v1",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.APIKey != "provider-secret-key" || runtime.APIKey == sealed {
		t.Fatal("runtime did not decrypt the database key")
	}
	if runtime.BaseURL != "https://models.example/v1" {
		t.Fatalf("base URL = %q", runtime.BaseURL)
	}
	if runtime.Sources.APIKey != AISourceDatabase || runtime.Sources.TextModel != AISourceDatabase {
		t.Fatalf("sources = %+v", runtime.Sources)
	}
}

func TestResolveAIRejectsPlaintextStoredKey(t *testing.T) {
	_, err := ResolveAI(AI{APIKey: "plaintext-key"}, nil, AI{
		BaseURL: "https://models.example/v1", TextModel: "text", VisionModel: "vision",
	}, false)
	if err == nil {
		t.Fatal("plaintext database key was accepted")
	}
}

func TestNormalizeAIBaseURLRequiresEncryptedTransport(t *testing.T) {
	for _, raw := range []string{
		"http://models.example/v1",
		"https://user:pass@models.example/v1",
		"https://models.example/v1?key=secret",
		"file:///tmp/model",
	} {
		if _, err := NormalizeAIBaseURL(raw, false); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	if got, err := NormalizeAIBaseURL("http://127.0.0.1:8081/v1/", true); err != nil || got != "http://127.0.0.1:8081/v1" {
		t.Fatalf("loopback development URL = (%q, %v)", got, err)
	}
}

func TestNormalizeAIAPIKeyRejectsWhitespace(t *testing.T) {
	if _, err := NormalizeAIAPIKey("secret key with spaces"); err == nil {
		t.Fatal("API key containing whitespace was accepted")
	}
	if got, err := NormalizeAIAPIKey("  valid-key-123  "); err != nil || got != "valid-key-123" {
		t.Fatalf("normalized key = (%q, %v)", got, err)
	}
}

func TestResolveAICarriesMaterialRuntimeLimits(t *testing.T) {
	stored := DefaultAI()
	stored.AgentMaxAnswerKB = 192
	stored.AgentToolScanMB = 96
	stored.MaterialMaxItems = 36
	stored.MaterialMaxFileMB = 18
	stored.MaterialMaxBatchMB = 240
	stored.MaterialMaxPDFPages = 24
	stored.MaterialConcurrency = 4
	stored.MaterialRetentionDays = 12
	stored.MaterialDailyBatches = 7
	stored.MaterialActiveBatches = 4
	stored.MaterialAllowedFormats = []string{"png", "pdf"}
	stored.AgentAttachmentMaxFileMB = 8
	stored.AgentAttachmentMaxMessageMB = 20
	stored.AgentAttachmentDailyMB = 120
	stored.AgentAttachmentMaxCount = 6
	runtime, err := ResolveAI(stored, nil, AI{
		BaseURL: "https://models.example/v1", APIKey: "provider-secret-key",
		TextModel: "text", VisionModel: "vision",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.AgentMaxAnswerKB != 192 || runtime.AgentToolScanMB != 96 || runtime.MaterialMaxItems != 36 || runtime.MaterialMaxFileMB != 18 || runtime.MaterialMaxBatchMB != 240 || runtime.MaterialMaxPDFPages != 24 || runtime.MaterialConcurrency != 4 || runtime.MaterialRetentionDays != 12 || runtime.MaterialDailyBatches != 7 || runtime.MaterialActiveBatches != 4 || strings.Join(runtime.MaterialAllowedFormats, ",") != "png,pdf" {
		t.Fatalf("material limits = %+v", runtime.AI)
	}
	if runtime.AgentAttachmentMaxFileMB != 8 || runtime.AgentAttachmentMaxMessageMB != 20 || runtime.AgentAttachmentDailyMB != 120 || runtime.AgentAttachmentMaxCount != 6 {
		t.Fatalf("attachment limits = %+v", runtime.AI)
	}
}

func TestNormalizeMaterialAllowedFormatsCanonicalizesAndRejectsEmpty(t *testing.T) {
	got, err := NormalizeMaterialAllowedFormats([]string{"PDF", "jpg", "png", "jpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "jpeg,png,pdf" {
		t.Fatalf("formats = %v", got)
	}
	if _, err := NormalizeMaterialAllowedFormats(nil); err == nil {
		t.Fatal("empty format list was accepted")
	}
	if _, err := NormalizeMaterialAllowedFormats([]string{"docx"}); err == nil {
		t.Fatal("unsupported format was accepted")
	}
}
