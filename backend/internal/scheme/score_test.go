package scheme

import (
	"math"
	"testing"
)

func f64(v float64) *float64 { return &v }

func TestScoreClaim(t *testing.T) {
	tests := []struct {
		name    string
		rule    ScoreRule
		claim   Claim
		want    Points
		wantErr bool
	}{
		{name: "per unit", rule: ScoreRule{Type: "per_unit", Per: 0.5, Cap: f64(5)}, claim: Claim{Quantity: f64(6)}, want: NewPoints(3)},
		{name: "per unit cap", rule: ScoreRule{Type: "per_unit", Per: 0.5, Cap: f64(5)}, claim: Claim{Quantity: f64(12)}, want: NewPoints(5)},
		{name: "legacy enum uses suggestion", rule: ScoreRule{Type: "enum", Options: []EnumOption{{Label: "省级", Score: 10}}}, claim: Claim{Option: "省级"}, want: NewPoints(10)},
		{name: "enum accepts adjusted expected score", rule: ScoreRule{Type: "enum", Options: []EnumOption{{Label: "省级", Score: 10}}}, claim: Claim{Option: "省级", Score: f64(5)}, want: NewPoints(5)},
		{name: "enum rejects score above rule max", rule: ScoreRule{Type: "enum", Options: []EnumOption{{Label: "省级", Score: 10}}}, claim: Claim{Option: "省级", Score: f64(11)}, wantErr: true},
		{name: "enum rejects negative score", rule: ScoreRule{Type: "enum", Options: []EnumOption{{Label: "省级", Score: 10}}}, claim: Claim{Option: "省级", Score: f64(-1)}, wantErr: true},
		{name: "enum rejects non-finite score", rule: ScoreRule{Type: "enum", Options: []EnumOption{{Label: "省级", Score: 10}}}, claim: Claim{Option: "省级", Score: f64(math.NaN())}, wantErr: true},
		{name: "unknown enum", rule: ScoreRule{Type: "enum", Options: []EnumOption{{Label: "省级", Score: 10}}}, claim: Claim{Option: "校级"}, wantErr: true},
		{name: "free negative allowed", rule: ScoreRule{Type: "free", Min: f64(-5), Max: f64(5)}, claim: Claim{Score: f64(-2)}, want: NewPoints(-2)},
		{name: "free preserves two decimal places", rule: ScoreRule{Type: "free", Min: f64(0), Max: f64(5)}, claim: Claim{Score: f64(0.25)}, want: NewPoints(0.25)},
		{name: "free above max", rule: ScoreRule{Type: "free", Min: f64(0), Max: f64(5)}, claim: Claim{Score: f64(6)}, wantErr: true},
		{name: "threshold reached", rule: ScoreRule{Type: "threshold", Unit: "项", Minimum: f64(2), Award: f64(5)}, claim: Claim{Quantity: f64(2)}, want: NewPoints(5)},
		{name: "threshold missed", rule: ScoreRule{Type: "threshold", Unit: "项", Minimum: f64(2), Award: f64(5)}, claim: Claim{Quantity: f64(1)}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ScoreClaim(tt.rule, tt.claim)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ScoreClaim() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ScoreClaim() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeCategoryScoreOrder(t *testing.T) {
	itemCap := NewPoints(5)
	items := []ScoredItem{
		{ID: 1, ItemKey: "volunteer", Score: NewPoints(7), ItemCap: &itemCap, CapGroupKey: "service", CapGroupCap: NewPoints(8)},
		{ID: 2, ItemKey: "news", Score: NewPoints(5), CapGroupKey: "service", CapGroupCap: NewPoints(8)},
		{ID: 3, ItemKey: "cadre_class", Score: NewPoints(2), ExclusiveGroup: "cadre"},
		{ID: 4, ItemKey: "cadre_school", Score: NewPoints(4), ExclusiveGroup: "cadre"},
	}
	base := []RecordedBase{
		{ItemKey: "discipline", Kind: "base", Score: NewPoints(70)},
		{ItemKey: "absence", Kind: "penalty", Score: NewPoints(-3)},
	}
	got := ComputeCategoryScore(items, base, NewPoints(75))
	if got.ItemTotal != NewPoints(12) { // shared cap 8 + exclusive max 4
		t.Fatalf("ItemTotal = %.3f, want 12", got.ItemTotal.Float64())
	}
	if got.BeforeCap != NewPoints(79) || got.Total != NewPoints(75) {
		t.Fatalf("BeforeCap/Total = %.3f/%.3f, want 79/75", got.BeforeCap.Float64(), got.Total.Float64())
	}
}

func TestExclusiveTieUsesStableID(t *testing.T) {
	items := []ScoredItem{
		{ID: 9, Score: NewPoints(4), ExclusiveGroup: "award"},
		{ID: 2, Score: NewPoints(4), ExclusiveGroup: "award"},
	}
	first := ComputeCategoryScore(items, nil, NewPoints(100))
	second := ComputeCategoryScore([]ScoredItem{items[1], items[0]}, nil, NewPoints(100))
	if first != second || first.Total != NewPoints(4) {
		t.Fatalf("exclusive selection is not deterministic: %#v %#v", first, second)
	}
}
