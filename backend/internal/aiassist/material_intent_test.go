package aiassist

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"easygpa/backend/internal/llm"
	"easygpa/backend/internal/scheme"
)

// Synthetic fixtures preserve the reported competition facts but contain no
// student identity, original certificate image, or production model response.
func intentScheme() scheme.Config {
	return scheme.Config{Categories: []scheme.Category{{
		Key: "practice", Name: "实践创新",
		Items: []scheme.Item{{
			Key: "contest", Name: "与专业学习相关的学科竞赛获奖",
			Note: "分类看教务处最新的 A 类竞赛目录，本表只定分值。目录逐年调整，按本届版本核对；不在目录内的按 A3 类减半。",
			ScoreRule: scheme.ScoreRule{Type: "enum", Options: []scheme.EnumOption{
				{Label: "国家级 A1 类·一等", Score: 30},
				{Label: "国家级 A2 类·一等", Score: 20},
				{Label: "国家级 A3 类·一等", Score: 15},
				{Label: "国家级 B 类·一等", Score: 10},
				{Label: "校级·一等", Score: 8},
			}},
		}},
	}}}
}

func intentCertificate() Observation {
	return Observation{
		AssetID: "certificate", MaterialType: "证书",
		RawText: "第十七届蓝桥杯全国软件和信息技术专业人才大赛。全国总决赛软件类 C/C++ 程序设计大学B组，全国总决赛一等奖。证书落款日期：2026年6月17日。",
		Fields: Fields{
			Title: "第十七届蓝桥杯全国软件和信息技术专业人才大赛", Date: "2026年6月17日",
			Level: "全国总决赛", Award: "一等奖", Group: "软件类 C/C++ 程序设计大学B组",
		},
	}
}

func intentCandidate(option string) Candidate {
	score := 999.0 // even a grounded tier must get its suggestion from Go
	return Candidate{
		ID: "candidate-1", Assets: []AssetRef{{AssetID: "certificate"}},
		CategoryKey: "practice", ItemKey: "contest",
		Title: "第十七届蓝桥杯全国总决赛一等奖", Note: "我参加了第十七届蓝桥杯全国总决赛软件类C/C++程序设计大学B组比赛，并获得一等奖。",
		Confidence: .99, Claim: scheme.Claim{Option: option, Score: &score}, ExpectedScore: &score,
	}
}

func composeIntent(t *testing.T, candidate Candidate, cfg scheme.Config, observations []Observation) Candidate {
	t.Helper()
	raw, err := json.Marshal(BatchDraft{Candidates: []Candidate{candidate}})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{responses: []llm.Response{{Content: string(raw)}}}
	p := &pipeline{client: client, cfg: Config{TextModel: "test-model"}}
	draft, err := p.ComposeChunk(context.Background(), observations, cfg, ComposeProgress{})
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Candidates) != 1 {
		t.Fatalf("expected the certificate to remain one draft, got %d", len(draft.Candidates))
	}
	return draft.Candidates[0]
}

func TestComposeDoesNotGuessSchoolCategoryFromCompetitionName(t *testing.T) {
	for _, option := range intentScheme().Categories[0].Items[0].ScoreRule.Options[:4] {
		t.Run(option.Label, func(t *testing.T) {
			proposed := intentCandidate(option.Label)
			got := composeIntent(t, proposed, intentScheme(), []Observation{intentCertificate()})
			if got.Claim.Option != "" || got.Claim.Score != nil || got.ExpectedScore != nil {
				t.Fatalf("ungrounded school category must not seed a score: %#v", got.Claim)
			}
			if got.CategoryKey != proposed.CategoryKey || got.ItemKey != proposed.ItemKey || got.Note != proposed.Note || got.Title != proposed.Title || len(got.Assets) != 1 {
				t.Fatal("missing directory must not discard the certificate, classification, or factual note")
			}
			if !got.NeedsReview || !slices.Contains(got.ReviewReasons, schoolCategoryReviewReason) {
				t.Fatal("missing school category needs an actionable review reason")
			}
		})
	}
}

func TestComposeUsesSuppliedSchoolCategoryInsteadOfHardCodingCompetition(t *testing.T) {
	for _, option := range intentScheme().Categories[0].Items[0].ScoreRule.Options[:4] {
		t.Run(option.Label, func(t *testing.T) {
			cfg := intentScheme()
			class := strings.ToUpper(schoolCategory(option.Label))
			quote := "本届已核对学校竞赛目录：第十七届蓝桥杯属于 " + class + " 类竞赛。"
			cfg.Categories[0].Items[0].Note += "\n" + quote
			proposed := intentCandidate(option.Label)
			proposed.OptionEvidence = &OptionEvidence{Source: "scheme_note", Activity: "蓝桥杯", Quote: quote}
			got := composeIntent(t, proposed, cfg, []Observation{intentCertificate()})
			if got.Claim.Option != option.Label || got.Claim.Score == nil || *got.Claim.Score != option.Score || got.ExpectedScore == nil || *got.ExpectedScore != option.Score {
				t.Fatalf("explicit school mapping must use that tier's suggestion, got %#v", got.Claim)
			}
			if !got.NeedsReview {
				t.Fatal("grounded AI suggestions still require the student's confirmation")
			}
		})
	}
}

func TestComposeRejectsUnsupportedOptionEvidence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		note        string
		activity    string
		quote       string
		source      string
		option      string
		assetID     string
		page        int
		observation *Observation
	}{
		{name: "fabricated quote", activity: "蓝桥杯", quote: "蓝桥杯属于A3类", source: "scheme_note"},
		{name: "fallback is not a mapping", activity: "蓝桥杯", quote: "不在目录内的按 A3 类减半", source: "scheme_note"},
		{name: "generic scoring table", note: "A3类竞赛全国总决赛一等奖建议15分。", activity: "竞赛", quote: "A3类竞赛全国总决赛一等奖建议15分。", source: "scheme_note"},
		{name: "generic award anchor", note: "另一项比赛一等奖属于A3类。", activity: "一等奖", quote: "另一项比赛一等奖属于A3类。", source: "scheme_note"},
		{name: "conditional quote cherry picking", note: "若蓝桥杯属于A3类，则按该类计分。", activity: "蓝桥杯", quote: "蓝桥杯属于A3类", source: "scheme_note"},
		{name: "pending quote cherry picking", note: "蓝桥杯属于A3类（待确认）。", activity: "蓝桥杯", quote: "蓝桥杯属于A3类", source: "scheme_note"},
		{name: "negated category", note: "蓝桥杯不是 A3 类竞赛。", activity: "蓝桥杯", quote: "蓝桥杯不是 A3 类竞赛。", source: "scheme_note"},
		{name: "must not recognize category", note: "蓝桥杯不得认定为 A3 类竞赛。", activity: "蓝桥杯", quote: "蓝桥杯不得认定为 A3 类竞赛。", source: "scheme_note"},
		{name: "enumerated alternatives", note: "蓝桥杯具体类别为A1类A2类A3类中的一类。", activity: "蓝桥杯", quote: "蓝桥杯具体类别为A1类A2类A3类中的一类。", source: "scheme_note"},
		{name: "ambiguous category quote cherry picking", note: "蓝桥杯属于 A2 类或 A3 类竞赛。", activity: "蓝桥杯", quote: "蓝桥杯属于 A2 类", source: "scheme_note", option: "国家级 A2 类·一等"},
		{name: "category does not match proposal", note: "蓝桥杯属于A3类竞赛。", activity: "蓝桥杯", quote: "蓝桥杯属于A3类竞赛。", source: "scheme_note", option: "国家级 A2 类·一等"},
		{name: "unrecognized evidence source", note: "蓝桥杯属于A3类竞赛。", activity: "蓝桥杯", quote: "蓝桥杯属于A3类竞赛。", source: "model_memory"},
		{name: "group B is not category B", activity: "蓝桥杯", quote: intentCertificate().RawText, source: "material", assetID: "certificate", option: "国家级 B 类·一等"},
		{name: "fields are not raw evidence", activity: "蓝桥杯", quote: "蓝桥杯属于A3类竞赛。", source: "material", assetID: "certificate", observation: &Observation{AssetID: "certificate", RawText: intentCertificate().RawText, Fields: Fields{Level: "蓝桥杯属于A3类竞赛。"}}},
		{name: "nonexistent material", activity: "蓝桥杯", quote: "蓝桥杯属于A3类竞赛。", source: "material", assetID: "invented"},
		{name: "wrong PDF page", activity: "蓝桥杯", quote: "蓝桥杯属于A3类竞赛。", source: "material", assetID: "certificate", page: 2, observation: &Observation{AssetID: "certificate", RawText: "蓝桥杯属于A3类竞赛。"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := intentScheme()
			if tc.note != "" {
				cfg.Categories[0].Items[0].Note = tc.note
			}
			option := tc.option
			if option == "" {
				option = "国家级 A3 类·一等"
			}
			proposed := intentCandidate(option)
			proposed.OptionEvidence = &OptionEvidence{Source: tc.source, Activity: tc.activity, Quote: tc.quote, AssetID: tc.assetID, Page: tc.page}
			observed := intentCertificate()
			if tc.observation != nil {
				observed = *tc.observation
			}
			got := composeIntent(t, proposed, cfg, []Observation{observed})
			if got.Claim.Option != "" || got.ExpectedScore != nil || got.OptionEvidence != nil {
				t.Fatalf("unsupported evidence was accepted for %s", tc.name)
			}
		})
	}
}

func TestComposeAcceptsReferencedDirectoryMaterial(t *testing.T) {
	quote := "蓝桥杯\nＡ３ 类"
	observations := []Observation{intentCertificate(), {AssetID: "directory", Page: 2, MaterialType: "学校竞赛目录", RawText: "本届教务处竞赛目录\n" + quote}}
	proposed := intentCandidate("国家级 A3 类·一等")
	proposed.Assets = append(proposed.Assets, AssetRef{AssetID: "directory", Page: 2})
	proposed.OptionEvidence = &OptionEvidence{Source: "material", Activity: "蓝桥杯", Quote: "蓝桥杯 Ａ３ 类", AssetID: "directory", Page: 2}
	got := composeIntent(t, proposed, intentScheme(), observations)
	if got.Claim.Option != proposed.Claim.Option || got.ExpectedScore == nil || *got.ExpectedScore != 15 {
		t.Fatal("the supplied school directory should ground A3 and its 15-point suggestion")
	}
	proposed.Assets = proposed.Assets[:1]
	got = composeIntent(t, proposed, intentScheme(), observations)
	if got.Claim.Option != "" || got.ExpectedScore != nil {
		t.Fatal("a directory outside the candidate's references cannot ground its school category")
	}
}

func TestSchoolCategoryEvidenceGateDoesNotBlockStudentCorrections(t *testing.T) {
	proposed := intentCandidate("国家级 A3 类·一等")
	proposed.OptionEvidence = nil // old drafts and frontend edits omit this field
	proposed.ReviewReasons = []string{schoolCategoryReviewReason, "请按班级评定学年核对证书日期"}
	proposed.NeedsReview = true
	studentScore := 15.0
	proposed.Claim.Score = &studentScore
	got := VerifyEditedCandidate(proposed, intentScheme(), map[string]struct{}{"certificate": {}})
	if got.Claim.Option != proposed.Claim.Option || got.ExpectedScore == nil || *got.ExpectedScore != studentScore {
		t.Fatal("an explicit valid student correction must not require AI-generated directory evidence")
	}
}

func TestSingleConfiguredSchoolCategoryStillNeedsEvidence(t *testing.T) {
	cfg := intentScheme()
	cfg.Categories[0].Items[0].ScoreRule.Options = cfg.Categories[0].Items[0].ScoreRule.Options[2:3]
	proposed := intentCandidate("国家级 A3 类·一等")
	got := composeIntent(t, proposed, cfg, []Observation{intentCertificate()})
	if got.Claim.Option != "" || got.ExpectedScore != nil {
		t.Fatal("one available tier does not prove that this competition belongs to it")
	}
	quote := "本届蓝桥杯属于 A3 类竞赛。"
	cfg.Categories[0].Items[0].Note = quote
	proposed.OptionEvidence = &OptionEvidence{Source: "scheme_note", Activity: "蓝桥杯", Quote: quote}
	got = composeIntent(t, proposed, cfg, []Observation{intentCertificate()})
	if got.Claim.Option != proposed.Claim.Option || got.ExpectedScore == nil || *got.ExpectedScore != 15 {
		t.Fatal("an evidenced event still qualifies when the rule has a single category")
	}
}

func TestSchoolCategoryEvidenceGatePreservesUnclassifiedSchoolTier(t *testing.T) {
	proposed := intentCandidate("校级·一等")
	got := composeIntent(t, proposed, intentScheme(), []Observation{intentCertificate()})
	if got.Claim.Option != proposed.Claim.Option || got.ExpectedScore == nil || *got.ExpectedScore != 8 {
		t.Fatal("tiers without school directory categories must retain their prefill behaviour")
	}
}

func TestPerceptionAndComposePromptsKeepCertificateFactsSeparate(t *testing.T) {
	client := &fakeClient{responses: []llm.Response{
		{Content: `{"materialType":"证书","rawText":"蓝桥杯大学B组一等奖","fields":{"level":"全国总决赛","award":"一等奖","group":"大学B组"}}`},
		{Content: `{"candidates":[],"warnings":[]}`},
	}}
	p := &pipeline{client: client, cfg: Config{VisionModel: "vision", TextModel: "text"}}
	observed, err := p.Perceive(context.Background(), Asset{ID: "certificate", MediaType: "image/png", Data: []byte("test")})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Fields.Group != "大学B组" || observed.Fields.Award != "一等奖" || observed.Fields.Level != "全国总决赛" {
		t.Fatal("the perception schema must not conflate group, award, and competition level")
	}
	if _, err := p.ComposeChunk(context.Background(), []Observation{observed}, intentScheme(), ComposeProgress{}); err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"大学B组不等于学校目录B类", "日期只抄录"} {
		if !strings.Contains(client.requests[0].Prompt, phrase) {
			t.Fatalf("vision prompt is missing %q", phrase)
		}
	}
	for _, phrase := range []string{"只有计分表不等于拿到了目录", "optionEvidence", "上传窗口和班级评定学年不是一回事", "不能使用常识"} {
		if !strings.Contains(client.requests[1].Prompt+client.requests[1].System, phrase) {
			t.Fatalf("compose prompt is missing %q", phrase)
		}
	}
}
