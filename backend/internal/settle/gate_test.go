package settle

import (
	"testing"
	"time"
)

func TestAcknowledgementAndFinalReviewAreNotSettlementGates(t *testing.T) {
	now := time.Now()
	in := GateInput{StudentCount: 3, SealedCount: 3, GPAImportedCount: 3, Now: now, WindowClose: now.Add(time.Hour)}
	gate := EvaluateGate(in)
	if !gate.Open {
		t.Fatalf("optional acknowledgement blocked settlement: %#v", gate)
	}
	for _, c := range gate.Conditions {
		if c.Key == "blindAuditComplete" || c.Key == "resultsConfirmed" {
			t.Fatalf("retired condition: %s", c.Key)
		}
	}
	in.PendingConflicts = 1
	if EvaluateGate(in).Open {
		t.Fatal("unresolved conflict must still block settlement")
	}
}
