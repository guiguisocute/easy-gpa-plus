package review

import (
	"errors"
	"testing"

	"easygpa/backend/internal/scheme"
)

func TestReconcile(t *testing.T) {
	tests := []struct {
		name     string
		expected int
		results  []Result
		status   Status
		conflict bool
	}{
		{name: "waiting for peer", expected: 2, results: []Result{{ReviewerID: 1, Decision: DecisionAccept, Score: scheme.NewPoints(5)}}, status: StatusConsensus},
		{name: "same result", expected: 2, results: []Result{{ReviewerID: 1, Decision: DecisionAccept, Score: scheme.NewPoints(5)}, {ReviewerID: 2, Decision: DecisionAccept, Score: scheme.NewPoints(5)}}, status: StatusScored},
		{name: "different score", expected: 2, results: []Result{{ReviewerID: 1, Decision: DecisionAdjust, Score: scheme.NewPoints(4)}, {ReviewerID: 2, Decision: DecisionAdjust, Score: scheme.NewPoints(5)}}, status: StatusArbitrating, conflict: true},
		{name: "self review exception", expected: 1, results: []Result{{ReviewerID: 2, Decision: DecisionAccept, Score: scheme.NewPoints(3)}}, status: StatusScored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Reconcile(tt.expected, tt.results)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.status || got.Conflict != tt.conflict {
				t.Fatalf("got %#v, want status=%s conflict=%v", got, tt.status, tt.conflict)
			}
		})
	}
}

func TestLockedIsTerminal(t *testing.T) {
	if err := Transition(StatusLocked, StatusScored); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("locked transition error = %v", err)
	}
}
