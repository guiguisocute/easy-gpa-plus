package api

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestMarshalSettlementValuePreservesContextAndCause(t *testing.T) {
	_, err := marshalSettlementValue("category scores", map[string]float64{"major": math.Inf(1)})
	if err == nil {
		t.Fatal("expected unsupported float to fail")
	}
	if !strings.Contains(err.Error(), "marshal settlement category scores") {
		t.Fatalf("error lacks field context: %v", err)
	}
	var unsupported *json.UnsupportedValueError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error does not preserve JSON root cause: %v", err)
	}
}

func TestMarshalSettlementValueReturnsValidJSON(t *testing.T) {
	raw, err := marshalSettlementValue("gate snapshot", map[string]any{"open": true, "pending": 0})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"open":true,"pending":0}` {
		t.Fatalf("snapshot = %s", raw)
	}
}
