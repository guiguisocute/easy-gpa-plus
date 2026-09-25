package scheme

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTemplateDropsRuntimeAndAppliesClassSettings(t *testing.T) {
	source := DefaultSelfReportConfig("v7")
	source.SchemeName = "学院评分细则"
	source.HonorRoll.TopPercent = 45

	template := TemplateFromConfig(source)
	raw, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"version"`, `"window"`, `"capabilities"`, `"honorRoll"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("canonical template contains runtime field %s: %s", forbidden, raw)
		}
	}
	if err := ValidateTemplate(template); err != nil {
		t.Fatalf("template is invalid: %v", err)
	}

	runtime := DefaultSelfReportConfig("draft")
	runtime.Window.Open = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	runtime.Window.Close = time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)
	runtime.HonorRoll.TopPercent = 20
	runtime.Capabilities.Submit = false

	merged := ApplyTemplate(runtime, template)
	if merged.SchemeName != source.SchemeName || len(merged.Categories) != len(source.Categories) {
		t.Fatalf("scoring data was not copied: %#v", merged)
	}
	if merged.HonorRoll.TopPercent != 20 || merged.Capabilities.Submit || !merged.Window.Open.Equal(runtime.Window.Open) {
		t.Fatalf("class runtime was not preserved: %#v", merged)
	}
}

func TestDecodeTemplateAcceptsLegacyFullScheme(t *testing.T) {
	legacy := DefaultSelfReportConfig("v3")
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	template, err := DecodeTemplate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if template.SchemeName != legacy.SchemeName || len(template.Categories) != len(legacy.Categories) {
		t.Fatalf("legacy scheme did not decode as a template: %#v", template)
	}
}
