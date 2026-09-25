package aievaluation

import (
	"testing"

	"easygpa/backend/internal/aiassist"
)

func TestCompareFieldsCountsOnlyLabeledValues(t *testing.T) {
	quantity := 3.0
	var got fieldMetrics
	compareFields(&got, aiassist.Fields{Date: "2026-08-09", Issuer: " 学 院 ", Quantity: &quantity}, labelFields{
		Date: "2026年8月9日", Issuer: "学院", Quantity: &quantity,
	})
	got.Overall.finish()
	if got.Overall.Total != 3 || got.Overall.Correct != 3 || got.Overall.Rate != 1 {
		t.Fatalf("unexpected fields metric: %#v", got.Overall)
	}
}

func TestGateRejectsSilentHighConfidenceError(t *testing.T) {
	value := metrics{
		Classification:             metricRate{Correct: 9, Total: 10, Rate: .9},
		Fields:                     fieldMetrics{Overall: metricRate{Correct: 9, Total: 10, Rate: .9}},
		SilentHighConfidenceErrors: 1,
	}
	got := evaluateGate(value)
	if got.Passed || len(got.Reasons) != 1 {
		t.Fatalf("unexpected gate: %#v", got)
	}
}
