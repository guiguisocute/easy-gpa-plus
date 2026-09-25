package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"easygpa/backend/internal/aiassist"
	"easygpa/backend/internal/config"
	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
)

func aiMaterialTestScheme(t *testing.T) storedScheme {
	t.Helper()
	current := testScheme()
	current.Config.Capabilities.Submit = true
	current.Config.Capabilities.Export.Gate = "settlement"
	current.Config.Categories[1].Items[0].ScoreRule.Options = []scheme.EnumOption{
		{Label: "国家级 A2 类一等", Score: 20},
		{Label: "国家级 A3 类一等", Score: 15},
	}
	if err := scheme.Validate(current.Config); err != nil {
		t.Fatal(err)
	}
	var err error
	current.Raw, err = json.Marshal(current.Config)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestRestoreAIBatchSchemeKeepsFrozenScoringAndCurrentTimeline(t *testing.T) {
	current := aiMaterialTestScheme(t)
	snapshot := append([]byte(nil), current.Raw...)
	var decoded scheme.Config
	if err := json.Unmarshal(snapshot, &decoded); err != nil {
		t.Fatal(err)
	}
	// This is how createAIBatch stores the snapshot after the class timeline
	// split. The previous apply gate failed even for every valid scoring tree.
	if !decoded.Window.Open.IsZero() || !decoded.Window.Close.IsZero() || scheme.Validate(decoded) == nil {
		t.Fatal("test must reproduce a scoring-only snapshot that needs its runtime timeline restored")
	}
	current.Config.Window.Close = current.Config.Window.Close.Add(24 * time.Hour)
	current.Config.Capabilities.Submit = false
	current.Config.HonorRoll = scheme.HonorRoll{TopPercent: 30, Awards: []scheme.Award{{Name: "测试档", TopPercent: 10}}}
	current.Config.Categories[1].Items[0].ScoreRule.Options[1].Score = 17
	frozen, err := restoreAIBatchScheme(current, snapshot)
	if err != nil {
		t.Fatalf("valid scoring-only snapshot rejected: %v", err)
	}
	if !reflect.DeepEqual(frozen.Config.Window, current.Config.Window) ||
		!reflect.DeepEqual(frozen.Config.Capabilities, current.Config.Capabilities) ||
		!reflect.DeepEqual(frozen.Config.HonorRoll, current.Config.HonorRoll) {
		t.Fatal("snapshot did not use the current class runtime envelope")
	}
	if score := frozen.Config.Categories[1].Items[0].ScoreRule.Options[1].Score; score != 15 {
		t.Fatalf("frozen A3 scoring changed to current value: %v", score)
	}
	if err := ensureCapability(frozen.Config, "submit", time.Now()); err == nil {
		t.Fatal("a disabled current submission capability must still block application")
	}
	if string(frozen.Raw) != string(snapshot) || strings.Contains(string(frozen.Raw), `"window"`) {
		t.Fatal("restoring the timeline must not persist it into the scoring snapshot")
	}
}

func TestRestoreAIBatchSchemeStillRejectsInvalidSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*scheme.Config)
	}{
		{"missing name", func(c *scheme.Config) { c.SchemeName = "" }},
		{"invalid weights", func(c *scheme.Config) { c.Weights["practice"] = 0.1 }},
		{"duplicate item", func(c *scheme.Config) { c.Categories[1].Items[1].Key = "practice_contest" }},
		{"empty enum", func(c *scheme.Config) { c.Categories[1].Items[0].ScoreRule.Options = nil }},
		{"duplicate enum", func(c *scheme.Config) {
			c.Categories[1].Items[0].ScoreRule.Options[1].Label = c.Categories[1].Items[0].ScoreRule.Options[0].Label
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := aiMaterialTestScheme(t)
			var invalid scheme.Config
			if err := json.Unmarshal(current.Raw, &invalid); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&invalid)
			raw, err := json.Marshal(invalid)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := restoreAIBatchScheme(current, raw); err == nil {
				t.Fatal("invalid scoring snapshot accepted")
			}
		})
	}
	current := aiMaterialTestScheme(t)
	if _, err := restoreAIBatchScheme(current, []byte(`{"schemeName":`)); err == nil {
		t.Fatal("malformed snapshot accepted")
	}
	current.Config.Window.Close = current.Config.Window.Open
	if _, err := restoreAIBatchScheme(current, current.Raw); err == nil {
		t.Fatal("invalid current class timeline accepted")
	}
}

func TestPrepareAICandidateAcceptsManualContestCorrection(t *testing.T) {
	current := aiMaterialTestScheme(t)
	frozen, err := restoreAIBatchScheme(current, current.Raw)
	if err != nil {
		t.Fatal(err)
	}
	candidate := aiassist.Candidate{
		ID: "contest", CategoryKey: "practice", ItemKey: "practice_contest", Title: "蓝桥杯全国总决赛一等奖",
		Claim: scheme.Claim{Option: "国家级 A3 类一等", Score: new(15.0)}, ExpectedScore: new(20.0),
		NeedsReview: true, ReviewReasons: []string{"具体竞赛类别和证书日期需要核对"},
		Assets: []aiassist.AssetRef{{AssetID: "1", Page: 0}, {AssetID: "1", Page: 1}},
	}
	verified, prepared, err := prepareAICandidate(frozen, candidate, map[string]aiAssetRecord{
		"1": {ID: 1, Status: "complete", MediaType: "application/pdf", PageCount: new(1)},
	}, time.Now())
	if err != nil {
		t.Fatalf("student correction was rejected: %v", err)
	}
	if verified.Claim.Option != "国家级 A3 类一等" || verified.Claim.Score == nil || *verified.Claim.Score != 15 ||
		verified.ExpectedScore == nil || *verified.ExpectedScore != 15 || prepared.Requested == nil || *prepared.Requested != 15 {
		t.Fatalf("manual A3 / 15 correction was not retained and recomputed: verified=%#v prepared=%#v", verified, prepared)
	}
	if !verified.NeedsReview || len(verified.ReviewReasons) == 0 {
		t.Fatal("advisory model uncertainty must remain visible, not prevent valid draft creation")
	}
	if len(verified.Assets) != 1 || verified.Assets[0].Page != 1 {
		t.Fatalf("duplicate page references were not normalized: %#v", verified.Assets)
	}
}

func TestPrepareAICandidateRejectsInvalidEditsAndForeignAssets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*aiassist.Candidate)
	}{
		{"unknown item", func(c *aiassist.Candidate) { c.ItemKey = "missing" }},
		{"wrong category", func(c *aiassist.Candidate) { c.CategoryKey = "moral" }},
		{"unknown tier", func(c *aiassist.Candidate) { c.Claim.Option = "国家级 A9 类一等" }},
		{"score above rule range", func(c *aiassist.Candidate) { c.Claim.Score = new(21.0) }},
		{"negative score", func(c *aiassist.Candidate) { c.Claim.Score = new(-1.0) }},
		{"missing assets", func(c *aiassist.Candidate) { c.Assets = nil }},
		{"foreign asset", func(c *aiassist.Candidate) { c.Assets[0].AssetID = "another-batch" }},
		{"mixed own and foreign assets", func(c *aiassist.Candidate) {
			c.Assets = append(c.Assets, aiassist.AssetRef{AssetID: "another-batch"})
		}},
		{"unfinished asset", func(c *aiassist.Candidate) { c.Assets[0].AssetID = "2" }},
		{"out of range free score", func(c *aiassist.Candidate) {
			c.ItemKey = "practice_paper"
			c.Claim = scheme.Claim{Score: new(13.0)}
		}},
		{"negative quantity", func(c *aiassist.Candidate) {
			c.CategoryKey, c.ItemKey = "moral", "moral_volunteer"
			c.Claim = scheme.Claim{Quantity: new(-1.0)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := aiassist.Candidate{
				CategoryKey: "practice", ItemKey: "practice_contest", Title: "测试竞赛",
				Claim:  scheme.Claim{Option: "国家级 A3 类一等", Score: new(15.0)},
				Assets: []aiassist.AssetRef{{AssetID: "1"}},
			}
			tc.mutate(&candidate)
			if _, _, err := prepareAICandidate(aiMaterialTestScheme(t), candidate, map[string]aiAssetRecord{
				"1": {ID: 1, Status: "complete", MediaType: "image/png"},
				"2": {ID: 2, Status: "failed", MediaType: "image/png"},
			}, time.Now()); err == nil {
				t.Fatal("invalid request was silently normalized into an accepted draft")
			}
		})
	}
}

func TestPrepareAICandidateStillAllowsUnfilledDraft(t *testing.T) {
	verified, prepared, err := prepareAICandidate(aiMaterialTestScheme(t), aiassist.Candidate{
		CategoryKey: "practice", ItemKey: "practice_contest", Title: "等待本人核对档位",
		Assets: []aiassist.AssetRef{{AssetID: "1"}},
	}, map[string]aiAssetRecord{"1": {ID: 1, Status: "complete", MediaType: "image/png"}}, time.Now())
	if err != nil {
		t.Fatalf("unfilled claim should still be allowed as a draft: %v", err)
	}
	if prepared.Requested != nil || verified.ExpectedScore != nil || !verified.NeedsReview {
		t.Fatal("unfilled draft must not acquire a score or lose its review warning")
	}
}

func TestValidateAIAssetClosedTypesAndLimit(t *testing.T) {
	allowed := opsconfig.DefaultMaterialAllowedFormats()
	valid := evidenceInput{Filename: "proof.webp", MediaType: "image/webp", SizeBytes: 2 * 1024 * 1024}
	if err := validateAIAsset(valid, 50, allowed); err != nil {
		t.Fatalf("valid WebP rejected: %v", err)
	}
	invalid := valid
	invalid.Filename = "proof.docx"
	invalid.MediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if err := validateAIAsset(invalid, 50, allowed); err == nil {
		t.Fatal("DOCX unexpectedly entered the AI pipeline")
	}
	valid.SizeBytes = 51 * 1024 * 1024
	if err := validateAIAsset(valid, 50, allowed); err == nil {
		t.Fatal("oversized AI asset was accepted")
	}
	valid.SizeBytes = 2 * 1024 * 1024
	if err := validateAIAsset(valid, 50, []string{"jpeg", "png", "pdf"}); err == nil {
		t.Fatal("WebP was accepted after operations disabled that format")
	}
}

func TestRequireAIUsesRuntimeSwitchAndEnvironmentFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{
		cfg: &config.Config{
			AppEnv: "dev", LLMBaseURL: "https://models.example/v1", LLMAPIKey: "provider-secret-key",
			LLMTextModel: "text-model", LLMVisionModel: "vision-model",
		},
		opsConfig: fakeRuntimeConfig{flags: opsconfig.Flags{AIEnabled: true}},
	}
	router := gin.New()
	router.GET("/", server.requireAI(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestRequireAIRejectsEnabledButIncompleteRuntimeConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{
		cfg: &config.Config{
			AppEnv: "dev", LLMBaseURL: "https://models.example/v1",
			LLMTextModel: "text-model", LLMVisionModel: "vision-model",
		},
		opsConfig: fakeRuntimeConfig{flags: opsconfig.Flags{AIEnabled: true}},
	}
	router := gin.New()
	router.GET("/", server.requireAI(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

// 页码只是界面上的标注，佐证按 assetId 复制整份原件，所以对不上的页码要就地修正而
// 不是把整批候选拒掉——提示词的示例写的就是 "page":0，模型给 PDF 照抄一个 0 曾让全部
// 候选在最后一步交不上去。
func TestNormaliseAIPageRefsRepairsInsteadOfRejecting(t *testing.T) {
	pages := 4
	assets := map[string]aiAssetRecord{
		"img": {MediaType: "image/png"},
		"pdf": {MediaType: "application/pdf", PageCount: &pages},
		"raw": {MediaType: "application/pdf"},
	}
	got := normaliseAIPageRefs([]aiassist.AssetRef{
		{AssetID: "img", Page: 3},
		{AssetID: "pdf", Page: 0},
		{AssetID: "pdf", Page: 1},
		{AssetID: "pdf", Page: 9},
		{AssetID: "raw", Page: 0},
	}, assets)
	want := []aiassist.AssetRef{
		{AssetID: "img", Page: 0},
		{AssetID: "pdf", Page: 1},
		{AssetID: "pdf", Page: 4},
		{AssetID: "raw", Page: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("refs = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("refs[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestRequireAIDisabledDoesNotReachHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/ai/batches", nil)
	server := &Server{cfg: &config.Config{AIEnabled: false}}
	middleware := server.requireAI()
	middleware(ctx)
	if recorder.Code != http.StatusNotImplemented || !ctx.IsAborted() {
		t.Fatalf("status = %d, aborted = %v", recorder.Code, ctx.IsAborted())
	}
}
