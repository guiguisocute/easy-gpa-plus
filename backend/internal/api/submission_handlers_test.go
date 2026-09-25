package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"easygpa/backend/internal/scheme"
)

// testScheme has two items per category on purpose: an earlier version of
// prepareSubmission stopped scanning after the first item, so everything except
// items[0] was rejected as "不在当前发布方案中".
func testScheme() storedScheme {
	cfg := scheme.Config{
		SchemeName: "测试方案",
		Version:    "v1",
		Window:     scheme.Window{Open: time.Now().Add(-time.Hour), Close: time.Now().Add(time.Hour)},
		Weights:    map[string]float64{"moral": 0.5, "practice": 0.5},
		Categories: []scheme.Category{
			{
				Key: "moral", Name: "思想品德", MaxTotal: 100,
				Items: []scheme.Item{
					{Key: "moral_volunteer", Name: "志愿服务", ScoreRule: scheme.ScoreRule{Type: "per_unit", Unit: "小时", Per: 0.5}},
					{Key: "moral_award", Name: "荣誉称号", ScoreRule: scheme.ScoreRule{Type: "enum", Options: []scheme.EnumOption{{Label: "校级", Score: 3}, {Label: "省级", Score: 6}}}},
				},
			},
			{
				Key: "practice", Name: "实践能力", MaxTotal: 100,
				BaseItems: []scheme.BaseItem{
					{Key: "practice_participation", Name: "参加两项学术科技活动", Full: 5, StudentClaim: &scheme.BaseStudentClaim{Minimum: 2, Unit: "项"}},
				},
				Items: []scheme.Item{
					{Key: "practice_contest", Name: "学科竞赛", ScoreRule: scheme.ScoreRule{Type: "enum", Options: []scheme.EnumOption{{Label: "省级一等", Score: 8}}}},
					{Key: "practice_paper", Name: "论文发表", ScoreRule: scheme.ScoreRule{Type: "free", Min: new(0.0), Max: new(12.0)}},
				},
			},
		},
	}
	return storedScheme{ID: 1, Version: 1, Name: cfg.SchemeName, Config: cfg}
}

func TestPrepareSubmissionFindsEveryItemNotJustTheFirst(t *testing.T) {
	current := testScheme()
	cases := []struct {
		category string
		itemKey  string
		claim    string
		want     float64
	}{
		{"moral", "moral_volunteer", `{"quantity":12}`, 6},
		{"moral", "moral_award", `{"option":"省级"}`, 6},
		{"moral", "moral_award", `{"option":"省级","score":3}`, 3},
		{"practice", "practice_contest", `{"option":"省级一等"}`, 8},
		{"practice", "practice_paper", `{"score":9}`, 9},
		{"practice", "practice_paper", `{"score":0.25}`, 0.25},
		{"practice", "practice_participation", `{"score":5}`, 5},
	}
	for _, tc := range cases {
		t.Run(tc.itemKey, func(t *testing.T) {
			prepared, err := prepareSubmission(current, submissionInput{
				Category: tc.category, ItemKey: tc.itemKey, Title: "标题", Claim: json.RawMessage(tc.claim),
			}, time.Now(), true)
			if err != nil {
				t.Fatalf("prepareSubmission(%s/%s) = %v, want no error", tc.category, tc.itemKey, err)
			}
			if prepared.Item.Key != tc.itemKey {
				t.Fatalf("matched item %q, want %q", prepared.Item.Key, tc.itemKey)
			}
			if prepared.Requested == nil || *prepared.Requested != tc.want {
				t.Fatalf("requested score = %v, want %v", prepared.Requested, tc.want)
			}
		})
	}
}

func TestPrepareSubmissionClaimableBaseAllowsPartialSelfReport(t *testing.T) {
	current := testScheme()
	prepared, err := prepareSubmission(current, submissionInput{
		Category: "practice", ItemKey: "practice_participation", Title: "只参加了一项比赛", Claim: json.RawMessage(`{"score":2.5}`),
	}, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Requested == nil || *prepared.Requested != 2.5 {
		t.Fatalf("requested score = %v, want 2.5", prepared.Requested)
	}
	if prepared.Item.ScoreRule.Type != "free" || prepared.Item.ScoreRule.Max == nil || *prepared.Item.ScoreRule.Max != 5 {
		t.Fatalf("claimable base rule = %#v, want free 0..5", prepared.Item.ScoreRule)
	}
	if !strings.Contains(prepared.Item.Note, "至少 2 项") {
		t.Fatalf("claimable base note lost its full-score condition: %q", prepared.Item.Note)
	}
}

func TestPrepareSubmissionRejectsNegativePerUnitQuantity(t *testing.T) {
	current := testScheme()
	_, err := prepareSubmission(current, submissionInput{
		Category: "moral", ItemKey: "moral_volunteer", Title: "负数量", Claim: json.RawMessage(`{"quantity":-1}`),
	}, time.Now(), false)
	var claimErr *scheme.ClaimError
	if !errors.As(err, &claimErr) || claimErr.Code != "claim_quantity_invalid" {
		t.Fatalf("negative quantity error = %v, want non-negative validation", err)
	}
}

func TestPrepareSubmissionOptionalSelfReportStaysUnknownUntilReview(t *testing.T) {
	for _, tc := range []struct{ category, item, claim string }{
		{"moral", "moral_volunteer", `{}`},
		{"moral", "moral_volunteer", `{"quantity":null}`},
		{"practice", "practice_paper", `{}`},
		{"practice", "practice_paper", `{"score":null}`},
		{"practice", "practice_participation", `{}`},
		{"moral", scheme.OtherSelfReportKey("moral"), `{}`},
	} {
		for _, submit := range []bool{false, true} {
			prepared, err := prepareSubmission(testScheme(), submissionInput{
				Category: tc.category, ItemKey: tc.item, Title: "待审核人根据佐证定分", Claim: json.RawMessage(tc.claim),
			}, time.Now(), submit)
			if err != nil || prepared.Requested != nil {
				t.Fatalf("%s claim=%s submit=%v: requested=%v err=%v, want NULL without error", tc.item, tc.claim, submit, prepared.Requested, err)
			}
		}
	}
	prepared, err := prepareSubmission(testScheme(), submissionInput{
		Category: "practice", ItemKey: "practice_paper", Title: "明确填零", Claim: json.RawMessage(`{"score":0}`),
	}, time.Now(), true)
	if err != nil || prepared.Requested == nil || *prepared.Requested != 0 {
		t.Fatalf("explicit zero must remain zero: %+v, %v", prepared, err)
	}
}

func TestPrepareSubmissionStillRejectsInvalidAndRequiredClaims(t *testing.T) {
	current := testScheme()
	current.Config.Categories[0].Items = append(current.Config.Categories[0].Items, scheme.Item{
		Key: "threshold", Name: "达标项", ScoreRule: scheme.ScoreRule{Type: "threshold", Unit: "次", Minimum: new(2.0), Award: new(5.0)},
	})
	for _, tc := range []struct{ category, item, claim, reason string }{
		{"practice", "practice_paper", `{"score":13}`, "claim_score_above_maximum"},
		{"practice", "practice_paper", `{"score":-1}`, "claim_score_below_minimum"},
		{"moral", "moral_volunteer", `{"quantity":-1}`, "claim_quantity_invalid"},
		{"moral", "moral_award", `{}`, "claim_option_invalid"},
		{"moral", "moral_award", `{"option":"已删除档位"}`, "claim_option_invalid"},
		{"moral", "threshold", `{}`, "claim_quantity_required"},
	} {
		_, err := prepareSubmission(current, submissionInput{
			Category: tc.category, ItemKey: tc.item, Title: "待校验", Claim: json.RawMessage(tc.claim),
		}, time.Now(), true)
		var claimErr *scheme.ClaimError
		if !errors.As(err, &claimErr) || claimErr.Code != tc.reason {
			t.Fatalf("%s %s: %v, want %s", tc.item, tc.claim, err, tc.reason)
		}
	}
}

// 佐证必须挂在已存在的记录上，所以先传图后写标题是完全正常的顺序：
// 草稿允许无标题，进审核那一步才必须有。
func TestPrepareSubmissionTitleRequiredOnlyForReview(t *testing.T) {
	current := testScheme()
	input := submissionInput{
		Category: "practice", ItemKey: "practice_paper", Title: "  ", Claim: json.RawMessage(`{"score":9}`),
	}
	if _, err := prepareSubmission(current, input, time.Now(), false); err != nil {
		t.Fatalf("untitled draft = %v, want accepted", err)
	}
	if _, err := prepareSubmission(current, input, time.Now(), true); err == nil {
		t.Fatal("expected an untitled submission to be rejected at review time")
	}
}

func TestPrepareSubmissionRejectsOverlongTitleEvenAsDraft(t *testing.T) {
	current := testScheme()
	if _, err := prepareSubmission(current, submissionInput{
		Category: "practice", ItemKey: "practice_paper", Title: strings.Repeat("标", 301), Claim: json.RawMessage(`{"score":9}`),
	}, time.Now(), false); err == nil {
		t.Fatal("expected a 301-character title to be rejected")
	}
}

func TestPrepareSubmissionRejectsItemFromAnotherCategory(t *testing.T) {
	current := testScheme()
	// practice_paper exists, but not under moral. Matching by item key alone
	// would let a student borrow another category's more generous rule.
	if _, err := prepareSubmission(current, submissionInput{
		Category: "moral", ItemKey: "practice_paper", Title: "标题", Claim: json.RawMessage(`{"score":9}`),
	}, time.Now(), true); err == nil {
		t.Fatal("expected cross-category item key to be rejected")
	}
}

func TestPrepareSubmissionSnapshotCarriesTheMatchedItem(t *testing.T) {
	current := testScheme()
	prepared, err := prepareSubmission(current, submissionInput{
		Category: "practice", ItemKey: "practice_paper", Title: "论文", Claim: json.RawMessage(`{"score":5}`),
	}, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ruleSnapshot
	if err := json.Unmarshal(prepared.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	// The snapshot is what审核 and 结算 read later; a wrong item here would let a
	// free-rule score be validated against some other item's bounds.
	if snapshot.Item.Key != "practice_paper" || snapshot.CategoryKey != "practice" {
		t.Fatalf("snapshot = %s/%s, want practice/practice_paper", snapshot.CategoryKey, snapshot.Item.Key)
	}
	if snapshot.Item.ScoreRule.Type != "free" {
		t.Fatalf("snapshot rule = %q, want free", snapshot.Item.ScoreRule.Type)
	}
}

func TestPrepareSubmissionAcceptsGeneratedOtherSelfReportItem(t *testing.T) {
	current := testScheme()
	key := scheme.OtherSelfReportKey("moral")
	prepared, err := prepareSubmission(current, submissionInput{
		Category: "moral", ItemKey: key, Title: "模板未列明的公益项目", Claim: json.RawMessage(`{"score":7}`),
	}, time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Item.Key != key || prepared.Item.Name != "其他项目自报" {
		t.Fatalf("fallback item = %q/%q", prepared.Item.Key, prepared.Item.Name)
	}
	if prepared.Requested == nil || *prepared.Requested != 7 {
		t.Fatalf("requested score = %v, want 7", prepared.Requested)
	}
	if prepared.Item.Evidence == nil || !prepared.Item.Evidence.Required {
		t.Fatal("fallback item must require evidence")
	}
}

func TestPrepareSubmissionAcceptsSystemDefaultSchemeCatchalls(t *testing.T) {
	cfg := scheme.DefaultSelfReportConfig("v1")
	current := storedScheme{ID: 1, Version: 1, Name: cfg.SchemeName, Config: cfg}
	for _, category := range cfg.Categories {
		t.Run(category.Key, func(t *testing.T) {
			prepared, err := prepareSubmission(current, submissionInput{
				Category: category.Key,
				ItemKey:  scheme.OtherSelfReportKey(category.Key),
				Title:    "未列入模板的材料",
				Claim:    json.RawMessage(`{"score":20}`),
			}, time.Now(), true)
			// 专业素质是教务导入的，连兜底自报都不该收：收下来也会在结算时被丢掉。
			if !category.AcceptsSubmissions() {
				if err == nil {
					t.Fatal("imported category must reject the catch-all self-report")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Requested == nil || *prepared.Requested != 20 {
				t.Fatalf("requested score = %v, want 20", prepared.Requested)
			}
			if prepared.Item.Evidence == nil || !prepared.Item.Evidence.Required {
				t.Fatal("system fallback must require evidence")
			}
		})
	}
}

func TestPrepareSubmissionRejectsFallbackKeyFromAnotherCategory(t *testing.T) {
	current := testScheme()
	if _, err := prepareSubmission(current, submissionInput{
		Category: "moral", ItemKey: scheme.OtherSelfReportKey("practice"), Title: "跨类项目", Claim: json.RawMessage(`{"score":7}`),
	}, time.Now(), true); err == nil {
		t.Fatal("expected cross-category fallback key to be rejected")
	}
}
