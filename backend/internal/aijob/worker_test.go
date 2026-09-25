package aijob

import (
	"context"
	"errors"
	"testing"
	"time"

	"easygpa/backend/internal/llm"
)

func TestRetryCallBacksOffForRetryableErrors(t *testing.T) {
	attempts := 0
	waits := make([]time.Duration, 0)
	result, err := retryCall(context.Background(), func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}, func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", &llm.CallError{StatusCode: 429, Retryable: true, Err: errors.New("limited")}
		}
		return "ok", nil
	})
	if err != nil || result != "ok" || attempts != 3 {
		t.Fatalf("retryCall = %q, %v, attempts %d", result, err, attempts)
	}
	if len(waits) != 2 || waits[0] != time.Second || waits[1] != 2*time.Second {
		t.Fatalf("waits = %v", waits)
	}
}

func TestRetryCallDoesNotRetryPermanentError(t *testing.T) {
	attempts := 0
	_, err := retryCall(context.Background(), func(context.Context, time.Duration) error {
		t.Fatal("unexpected wait")
		return nil
	}, func() (string, error) {
		attempts++
		return "", errors.New("invalid image")
	})
	if err == nil || attempts != 1 {
		t.Fatalf("err = %v, attempts = %d", err, attempts)
	}
}

func TestBatchLockKeyIsStableAndNamespaced(t *testing.T) {
	if batchLockKey(7) == 0 || batchLockKey(7) != batchLockKey(7) || batchLockKey(7) == batchLockKey(8) {
		t.Fatal("batch lock key is not stable")
	}
}

func TestWorkerUsesRuntimeMaterialLimits(t *testing.T) {
	worker := &Worker{cfg: Config{
		Concurrency: 2, MaxItems: 100, MaxPDFPages: 64,
		RuntimeLimits: func(context.Context) (Limits, error) {
			return Limits{Concurrency: 5, MaxItems: 42, MaxPDFPages: 28}, nil
		},
	}}
	limits, err := worker.limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if limits.Concurrency != 5 || limits.MaxItems != 42 || limits.MaxPDFPages != 28 {
		t.Fatalf("runtime limits = %+v", limits)
	}
}
