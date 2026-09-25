package scheme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCanonicalExampleTemplateIsReusable(t *testing.T) {
	filename := filepath.Join("..", "..", "..", "examples", "scoring-scheme.json")
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read canonical scheme: %v", err)
	}
	var template Template
	if err := json.Unmarshal(raw, &template); err != nil {
		t.Fatalf("decode canonical template: %v", err)
	}
	if err := ValidateTemplate(template); err != nil {
		t.Fatalf("canonical template cannot be reused: %v", err)
	}
	fallback := DefaultSelfReportConfig("v1")
	if !reflect.DeepEqual(fallback.Weights, template.Weights) {
		t.Fatalf("default weights differ from canonical template: %#v != %#v", fallback.Weights, template.Weights)
	}
}
