package api

import (
	"testing"
	"time"

	"easygpa/backend/internal/scheme"
)

func TestNextStudentAppealRound(t *testing.T) {
	tests := []struct {
		name     string
		previous *appealRecord
		want     int
		wantErr  bool
	}{
		{name: "first appeal", want: 1},
		{name: "second after reviewer consensus", previous: &appealRecord{Round: 1, Status: "resolved"}, want: 2},
		{name: "cannot continue after admin arbitration", previous: &appealRecord{Round: 1, Status: "final"}, wantErr: true},
		{name: "cannot overlap pending first appeal", previous: &appealRecord{Round: 1, Status: "reviewing"}, wantErr: true},
		{name: "cannot file a third appeal", previous: &appealRecord{Round: 2, Status: "final"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := nextStudentAppealRound(tt.previous)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got round %d", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got round %d, err %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestReconcileAppealScores(t *testing.T) {
	tests := []struct {
		name         string
		expected     int
		scores       []float64
		wantStatus   string
		wantScore    *float64
		wantConflict bool
		wantErr      bool
	}{
		{name: "waiting for peer", expected: 2, scores: []float64{8}, wantStatus: "reviewing"},
		{name: "pair agrees", expected: 2, scores: []float64{8, 8}, wantStatus: "resolved", wantScore: floatPointer(8)},
		{name: "millipoint normalization", expected: 2, scores: []float64{8.0001, 8.0004}, wantStatus: "resolved", wantScore: floatPointer(8.0001)},
		{name: "pair conflicts", expected: 2, scores: []float64{8, 7.5}, wantStatus: "escalated", wantConflict: true},
		{name: "invalid expected count", expected: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, score, conflict, err := reconcileAppealScores(tt.expected, tt.scores)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || status != tt.wantStatus || conflict != tt.wantConflict {
				t.Fatalf("got status=%s conflict=%v err=%v", status, conflict, err)
			}
			if (score == nil) != (tt.wantScore == nil) {
				t.Fatalf("got score %v, want %v", score, tt.wantScore)
			}
			if score != nil && *score != *tt.wantScore {
				t.Fatalf("got score %v, want %v", *score, *tt.wantScore)
			}
		})
	}
}

func TestUpholdScoreMatchesForReviewerAndAdminPaths(t *testing.T) {
	original := 8.0
	sameAtStoredPrecision := 8.0004
	different := 7.5
	if !upholdScoreMatches("uphold", &sameAtStoredPrecision, &original) {
		t.Fatal("equivalent millipoint score should be accepted for uphold")
	}
	if upholdScoreMatches("uphold", &different, &original) {
		t.Fatal("changed score should be rejected for uphold")
	}
	if !upholdScoreMatches("adjust", &different, &original) {
		t.Fatal("adjust decision should permit a changed score")
	}
}

func TestReconcileAppealOutcomesRequiresClassificationAndScoreConsensus(t *testing.T) {
	tests := []struct {
		name         string
		outcomes     []appealReviewOutcome
		wantStatus   string
		wantConflict bool
		wantOutcome  bool
	}{
		{name: "waiting for peer", outcomes: []appealReviewOutcome{{Category: "moral", ItemKey: "service", Score: 4}}, wantStatus: "reviewing"},
		{name: "classification and score agree", outcomes: []appealReviewOutcome{{Category: "moral", ItemKey: "service", Score: 4}, {Category: "moral", ItemKey: "service", Score: 4.0004}}, wantStatus: "resolved", wantOutcome: true},
		{name: "same category different item conflicts", outcomes: []appealReviewOutcome{{Category: "moral", ItemKey: "service", Score: 4}, {Category: "moral", ItemKey: "honor", Score: 4}}, wantStatus: "escalated", wantConflict: true},
		{name: "different category conflicts", outcomes: []appealReviewOutcome{{Category: "moral", ItemKey: "service", Score: 4}, {Category: "practice", ItemKey: "service", Score: 4}}, wantStatus: "escalated", wantConflict: true},
		{name: "different score conflicts", outcomes: []appealReviewOutcome{{Category: "moral", ItemKey: "service", Score: 4}, {Category: "moral", ItemKey: "service", Score: 3.5}}, wantStatus: "escalated", wantConflict: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, outcome, conflict, err := reconcileAppealOutcomes(2, tt.outcomes)
			if err != nil || status != tt.wantStatus || conflict != tt.wantConflict || (outcome != nil) != tt.wantOutcome {
				t.Fatalf("got status=%s outcome=%#v conflict=%v err=%v", status, outcome, conflict, err)
			}
		})
	}
}

func TestUpholdOutcomeMatchesClassificationAndScore(t *testing.T) {
	category, item, score := "moral", "service", 4.0
	row := appealRecord{OriginalCategory: &category, OriginalItem: &item, OriginalScore: &score}
	if !upholdOutcomeMatches("uphold", appealReviewOutcome{Category: category, ItemKey: item, Score: 4.0004}, row) {
		t.Fatal("equivalent original classification and stored-precision score should uphold")
	}
	if upholdOutcomeMatches("uphold", appealReviewOutcome{Category: category, ItemKey: "honor", Score: score}, row) {
		t.Fatal("uphold must reject a changed item")
	}
	if upholdOutcomeMatches("uphold", appealReviewOutcome{Category: "practice", ItemKey: item, Score: score}, row) {
		t.Fatal("uphold must reject a changed category")
	}
	if upholdOutcomeMatches("uphold", appealReviewOutcome{Category: category, ItemKey: item, Score: 3.5}, row) {
		t.Fatal("uphold must reject a changed score")
	}
}

func TestClaimableSchemeItemIncludesOnlyStudentClaimablePublishedTargets(t *testing.T) {
	min, max := 0.0, 10.0
	cfg := scheme.Config{Categories: []scheme.Category{{
		Key: "moral", Name: "思想品德", MaxTotal: 20,
		Items: []scheme.Item{{Key: "service", Name: "志愿服务", ScoreRule: scheme.ScoreRule{Type: "free", Min: &min, Max: &max}}},
		BaseItems: []scheme.BaseItem{
			{Key: "claimable_base", Name: "可申报基础项", Full: 5, StudentClaim: &scheme.BaseStudentClaim{Minimum: 1, Unit: "次"}},
			{Key: "admin_base", Name: "管理员基础项", Full: 5},
		},
	}}}
	for _, itemKey := range []string{"service", "claimable_base", scheme.OtherSelfReportKey("moral")} {
		if _, _, ok := claimableSchemeItem(cfg, "moral", itemKey); !ok {
			t.Fatalf("expected %s to be claimable", itemKey)
		}
	}
	if _, _, ok := claimableSchemeItem(cfg, "moral", "admin_base"); ok {
		t.Fatal("reviewer-maintained base item must not be a classification target")
	}
	if _, _, ok := claimableSchemeItem(cfg, "moral", "later_version_item"); ok {
		t.Fatal("an item absent from the submission scheme must not be accepted")
	}
}

func TestAppealEvidenceEditable(t *testing.T) {
	resolvedAt := time.Now()
	tests := []struct {
		name string
		row  appealRecord
		want bool
	}{
		{name: "draft", row: appealRecord{Kind: "student_appeal", Status: "draft"}, want: true},
		{name: "filed", row: appealRecord{Kind: "student_appeal", Status: "filed"}, want: true},
		{name: "first round escalated", row: appealRecord{Kind: "student_appeal", Round: 1, Status: "escalated"}, want: true},
		{name: "second round escalated", row: appealRecord{Kind: "student_appeal", Round: 2, Status: "escalated"}, want: true},
		{name: "resolved escalation", row: appealRecord{Kind: "student_appeal", Status: "escalated", ResolvedAt: &resolvedAt}},
		{name: "final", row: appealRecord{Kind: "student_appeal", Status: "final"}},
		{name: "objection", row: appealRecord{Kind: "rule_objection", Status: "draft"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appealEvidenceEditable(tt.row); got != tt.want {
				t.Fatalf("appealEvidenceEditable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppealWithdrawable(t *testing.T) {
	tests := []struct {
		name    string
		row     appealRecord
		decided int
		want    bool
	}{
		{name: "filed before assignment", row: appealRecord{Kind: "student_appeal", Status: "filed"}, want: true},
		{name: "reviewing before any decision", row: appealRecord{Kind: "student_appeal", Status: "reviewing"}, want: true},
		{name: "reviewer already decided", row: appealRecord{Kind: "student_appeal", Status: "reviewing"}, decided: 1},
		{name: "escalated", row: appealRecord{Kind: "student_appeal", Status: "escalated"}},
		{name: "resolved", row: appealRecord{Kind: "student_appeal", Status: "resolved"}},
		{name: "other appeal kind", row: appealRecord{Kind: "rule_objection", Status: "filed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appealWithdrawable(tt.row, tt.decided); got != tt.want {
				t.Fatalf("appealWithdrawable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }
