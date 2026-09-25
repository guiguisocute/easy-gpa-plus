package scheme

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultSelfReportConfig(t *testing.T) {
	cfg := DefaultSelfReportConfig("v1")
	if err := Validate(cfg); err != nil {
		t.Fatalf("default self-report config is invalid: %v", err)
	}
	if cfg.Version != "v1" {
		t.Fatalf("version = %q, want v1", cfg.Version)
	}
	want := []struct {
		key, name     string
		base, penalty int
	}{
		{key: "major", name: "专业素质"},
		{key: "moral", name: "思想道德素质", base: 1, penalty: 1},
		{key: "practice", name: "实践创新素质", base: 1},
		{key: "health", name: "身体心理素质", base: 1},
	}
	if len(cfg.Categories) != len(want) {
		t.Fatalf("categories = %d, want %d", len(cfg.Categories), len(want))
	}
	for i, expected := range want {
		category := cfg.Categories[i]
		if category.Key != expected.key || category.Name != expected.name {
			t.Fatalf("category[%d] = %q/%q, want %q/%q", i, category.Key, category.Name, expected.key, expected.name)
		}
		if len(category.BaseItems) != expected.base || len(category.PenaltyItems) != expected.penalty || len(category.Items) != 0 {
			t.Fatalf("category %q items = base:%d penalty:%d regular:%d, want base:%d penalty:%d regular:0", category.Key, len(category.BaseItems), len(category.PenaltyItems), len(category.Items), expected.base, expected.penalty)
		}
		if category.BaseItems == nil || category.PenaltyItems == nil || category.Items == nil {
			t.Fatalf("category %q arrays must be non-nil for the frontend contract", category.Key)
		}
		item := OtherSelfReportItem(category)
		if item.Name != "其他项目自报" || item.ScoreRule.Type != "free" {
			t.Fatalf("catch-all for %q = %#v", category.Key, item)
		}
		if item.ScoreRule.Min == nil || *item.ScoreRule.Min != 0 || item.ScoreRule.Max == nil || *item.ScoreRule.Max != category.MaxTotal {
			t.Fatalf("catch-all bounds for %q = %#v", category.Key, item.ScoreRule)
		}
		if item.Evidence == nil || !item.Evidence.Required {
			t.Fatalf("catch-all for %q must require evidence", category.Key)
		}
	}
	if cfg.Weights["major"] != 0.6 || cfg.Weights["moral"] != 0.15 || cfg.Weights["practice"] != 0.15 || cfg.Weights["health"] != 0.1 {
		t.Fatalf("unexpected default weights: %#v", cfg.Weights)
	}
	moralBase := 0.0
	for _, item := range cfg.Categories[1].BaseItems {
		moralBase += item.Full
	}
	if moralBase != 70 {
		t.Fatalf("moral automatic base = %v, want 70", moralBase)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"items":null`) || strings.Contains(string(raw), `"baseItems":null`) || strings.Contains(string(raw), `"penaltyItems":null`) {
		t.Fatalf("default config must expose empty arrays, got %s", raw)
	}
}

func TestStudentClaimBaseNoteListsFullScoreCondition(t *testing.T) {
	base := BaseItem{
		Key: "practice_base", Name: "参加课外学术科技活动", Full: 40,
		StudentClaim: &BaseStudentClaim{Minimum: 2, Unit: "项"},
		Note:         "竞赛、论文、专利均可计入。",
	}
	item, ok := StudentClaimBaseItem(base)
	if !ok {
		t.Fatal("expected claimable base item")
	}
	if !strings.Contains(item.Note, "至少 2 项可得 40 分") {
		t.Fatalf("missing full-score condition: %q", item.Note)
	}
	if !strings.HasPrefix(strings.TrimSpace(item.Note), "- ") {
		t.Fatalf("student-facing note should be markdown list, got %q", item.Note)
	}
	if !strings.Contains(item.Note, "竞赛、论文、专利均可计入") {
		t.Fatalf("custom note lost: %q", item.Note)
	}
}
