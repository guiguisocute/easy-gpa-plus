package api

import "testing"

func TestPlanKnowledgeCompletion(t *testing.T) {
	tests := []struct {
		name              string
		processingEnabled bool
		wantStatus        string
		wantProcess       bool
	}{
		{name: "processing enabled", processingEnabled: true, wantStatus: "queued", wantProcess: true},
		{name: "static storage only", processingEnabled: false, wantStatus: "unsupported", wantProcess: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := planKnowledgeCompletion(test.processingEnabled)
			if got.Status != test.wantStatus || got.Process != test.wantProcess {
				t.Fatalf("planKnowledgeCompletion(%t) = %#v, want status=%q process=%t", test.processingEnabled, got, test.wantStatus, test.wantProcess)
			}
		})
	}
}
