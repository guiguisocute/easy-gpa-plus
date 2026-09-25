package worker

import (
	"context"
	"errors"
	"testing"
)

func TestFinishWorkerTreatsCancellationAsCleanShutdown(t *testing.T) {
	if err := finishWorker(context.Canceled); err != nil {
		t.Fatalf("canceled worker returned %v", err)
	}
	want := errors.New("worker failed")
	if err := finishWorker(want); !errors.Is(err, want) {
		t.Fatalf("worker error = %v, want %v", err, want)
	}
}
