package opsconfig

import (
	"testing"

	"easygpa/backend/internal/llm"
)

func TestNormalizeModelParametersAllowlist(t *testing.T) {
	parameters, err := NormalizeModelParameters(map[string]any{
		"temperature": 0.35,
		"maxTokens":   float64(4096),
	})
	if err != nil {
		t.Fatal(err)
	}
	if parameters["temperature"] != 0.35 || parameters["maxTokens"] != int64(4096) {
		t.Fatalf("normalized parameters = %#v", parameters)
	}

	invalid := []map[string]any{
		{"topP": 0.9},
		{"temperature": 2.01},
		{"maxTokens": 1.5},
		{"maxTokens": 131073},
	}
	for _, input := range invalid {
		if _, err := NormalizeModelParameters(input); err == nil {
			t.Fatalf("invalid parameters accepted: %#v", input)
		}
	}
}

func TestModelBindingAppliesRouteParameters(t *testing.T) {
	binding := ModelBinding{Parameters: map[string]any{"temperature": 0.7, "maxTokens": int64(2048)}}
	request := llm.Request{}
	binding.ApplyParameters(&request)
	if request.Temperature != 0.7 || request.MaxTokens != 2048 {
		t.Fatalf("route parameters were not applied: %#v", request)
	}
}

func TestNormalizeEvidenceAllowedFormats(t *testing.T) {
	formats, err := NormalizeEvidenceAllowedFormats([]string{".PNG", "pdf", "png"})
	if err != nil {
		t.Fatal(err)
	}
	if len(formats) != 2 || formats[0] != "pdf" || formats[1] != "png" {
		t.Fatalf("normalized formats = %#v", formats)
	}
	if _, err := NormalizeEvidenceAllowedFormats([]string{"exe"}); err == nil {
		t.Fatal("unknown executable format was accepted")
	}
	if _, err := NormalizeEvidenceAllowedFormats(nil); err == nil {
		t.Fatal("empty platform allowlist was accepted")
	}
}

func TestValidateLifecycleSafeRanges(t *testing.T) {
	policy := DefaultLifecycle()
	if _, err := ValidateLifecycle(policy); err != nil {
		t.Fatalf("default lifecycle is invalid: %v", err)
	}
	policy.StorageReconcileMinutes = 4
	if _, err := ValidateLifecycle(policy); err == nil {
		t.Fatal("unsafe storage reconciliation interval was accepted")
	}
	policy = DefaultLifecycle()
	policy.BackupSchedule = "25:00"
	if _, err := ValidateLifecycle(policy); err == nil {
		t.Fatal("invalid backup schedule was accepted")
	}
}

func TestNormalizeResourceLimitsClampsUnsafeValues(t *testing.T) {
	flags := DefaultFlags()
	flags.RequestConcurrency = 100000
	flags.ExportConcurrency = 100000
	flags.PasswordHashConcurrency = 100000
	flags.APIRateLimitPerMinute = 50
	flags.APIRateLimitBurst = 100000
	flags.EvidenceDailyMB = 100000
	flags.normalize()
	if flags.RequestConcurrency != 512 || flags.ExportConcurrency != 8 || flags.PasswordHashConcurrency != 16 || flags.APIRateLimitBurst != 50 || flags.EvidenceDailyMB != 10000 {
		t.Fatalf("normalized flags = %+v", flags)
	}
	ai := DefaultAI()
	ai.MaterialDailyBatches = 100000
	ai.MaterialActiveBatches = 100000
	ai.AgentMaxAnswerKB = 100000
	ai.AgentToolScanMB = 100000
	ai.normalize()
	if ai.MaterialDailyBatches != 100 || ai.MaterialActiveBatches != 20 || ai.AgentMaxAnswerKB != 512 || ai.AgentToolScanMB != 256 {
		t.Fatalf("normalized AI limits = %+v", ai)
	}
}
