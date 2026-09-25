package aiassist

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easygpa/backend/internal/scheme"
)

// This opt-in regression uses synthetic facts, never production batches. Set
// EASYGPA_LIVE_MATERIAL_INTENT=1 and EASYGPA_LIVE_MODEL_KEY to enable it. The URL
// and model may be supplied through EASYGPA_LIVE_MODEL_URL / MODEL respectively.
// An optional EASYGPA_LIVE_MATERIAL_IMAGE is a synthetic PNG text card containing
// these same facts; it checks Perceive independently. Composition uses the
// report transcription, not the image's "not a real certificate" watermark.
// Never commit keys, real certificates, or raw model responses.
func TestLiveMaterialCompetitionIntent(t *testing.T) {
	if os.Getenv("EASYGPA_LIVE_MATERIAL_INTENT") != "1" {
		t.Skip("opt-in synthetic live material regression")
	}
	key := os.Getenv("EASYGPA_LIVE_MODEL_KEY")
	if key == "" {
		t.Fatal("EASYGPA_LIVE_MODEL_KEY is required for this opt-in test")
	}
	baseURL := os.Getenv("EASYGPA_LIVE_MODEL_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model := os.Getenv("EASYGPA_LIVE_MODEL")
	if model == "" {
		model = "deepseek-v4-flash-vision-exp"
	}
	client, err := liveClient(baseURL, key)
	if err != nil {
		t.Fatal(strings.ReplaceAll(err.Error(), key, "[redacted]"))
	}
	p := New(Config{
		Enabled: true, VisionModel: model, TextModel: model,
		StudentName: "测试同学", StudentSID: "0000",
	}, client)
	observation := Observation{
		AssetID: "synthetic-contest", MaterialType: "证书",
		RawText: "测试同学：第十七届蓝桥杯全国软件和信息技术专业人才大赛，全国总决赛，软件类 C/C++ 程序设计大学B组，全国总决赛一等奖。证书落款日期：2026年6月17日。",
		Fields:  Fields{Title: "第十七届蓝桥杯全国总决赛", Date: "2026年6月17日", Level: "全国总决赛一等奖"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if imagePath := os.Getenv("EASYGPA_LIVE_MATERIAL_IMAGE"); imagePath != "" {
		data, err := os.ReadFile(imagePath)
		if err != nil {
			t.Fatal("cannot read the synthetic image fixture")
		}
		observed, err := p.Perceive(ctx, Asset{ID: observation.AssetID, MediaType: "image/png", Data: data})
		if err != nil {
			t.Fatal(strings.ReplaceAll(err.Error(), key, "[redacted]"))
		}
		for _, fact := range []string{"蓝桥杯", "一等奖", "大学B组"} {
			if !strings.Contains(strings.ReplaceAll(observed.RawText, " ", ""), fact) {
				t.Errorf("vision omitted a required synthetic fact: %s", fact)
			}
		}
		t.Logf("synthetic vision: model=%s tokens=%d duration_ms=%d", observed.Model, observed.Usage.TotalTokens, observed.DurationMS)
	}
	for _, tc := range []struct {
		name      string
		directory string
		wantTier  string
	}{
		{name: "directory_unknown"},
		{
			name:      "published_scheme_confirms_A3",
			directory: "\n本届已核对教务处竞赛目录：第十七届蓝桥杯全国软件和信息技术专业人才大赛属于 A3 类竞赛。全国总决赛一等奖对应国家级 A3 类·一等。",
			wantTier:  "国家级 A3 类·一等",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "scoring-scheme.json"))
			if err != nil {
				t.Fatal(err)
			}
			var cfg scheme.Config
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			found := false
			for i := range cfg.Categories {
				for j := range cfg.Categories[i].Items {
					if cfg.Categories[i].Items[j].Key == "practice_contest" {
						cfg.Categories[i].Items[j].Note += tc.directory
						found = true
					}
				}
			}
			if !found {
				t.Fatal("canonical competition scoring item is missing")
			}
			draft, err := p.ComposeBatch(ctx, []Observation{observation}, cfg, ComposeProgress{})
			if err != nil {
				t.Fatal(strings.ReplaceAll(err.Error(), key, "[redacted]"))
			}
			if len(draft.Candidates) != 1 {
				t.Fatalf("one synthetic competition must produce one candidate, got %d", len(draft.Candidates))
			}
			candidate := draft.Candidates[0]
			if candidate.CategoryKey != "practice" || candidate.ItemKey != "practice_contest" {
				t.Fatalf("wrong scoring item: %s/%s", candidate.CategoryKey, candidate.ItemKey)
			}
			if candidate.Claim.Option != tc.wantTier {
				t.Errorf("tier = %q, want %q", candidate.Claim.Option, tc.wantTier)
			}
			if tc.wantTier == "" {
				if candidate.ExpectedScore != nil || candidate.Claim.Score != nil || !candidate.NeedsReview {
					t.Error("unknown directory must require tier selection without guessing a score")
				}
			} else if candidate.ExpectedScore == nil || *candidate.ExpectedScore != 15 {
				t.Error("the explicitly supported A3 tier must suggest 15")
			}
			if len(candidate.Assets) != 1 || candidate.Assets[0].AssetID != observation.AssetID {
				t.Error("the candidate must retain its source material")
			}
			t.Logf("compose: model=%s tokens=%d duration_ms=%d suggested_tier=%q", draft.Model, draft.Usage.TotalTokens, draft.DurationMS, candidate.Claim.Option)
			score := 15.0
			candidate.Claim = scheme.Claim{Option: "国家级 A3 类·一等", Score: &score}
			edited := VerifyEditedCandidate(candidate, cfg, map[string]struct{}{observation.AssetID: {}})
			if edited.ExpectedScore == nil || *edited.ExpectedScore != 15 || edited.Claim.Option != candidate.Claim.Option {
				t.Error("manual A3 / 15 correction must survive deterministic verification")
			}
		})
	}
}
