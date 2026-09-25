package api

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMarshalAuditValuePreservesSerializationErrors(t *testing.T) {
	_, err := marshalAuditValue("metadata", map[string]any{"unsupported": make(chan int)})
	if err == nil {
		t.Fatal("expected unsupported audit value to fail")
	}
	var unsupported *json.UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error %T does not preserve json.UnsupportedTypeError: %v", err, err)
	}
}

func TestMarshalAuditValueNamesTheFailedField(t *testing.T) {
	_, err := marshalAuditValue("before_data", make(chan int))
	if err == nil || err.Error() != "marshal audit before_data: json: unsupported type: chan int" {
		t.Fatalf("unexpected error: %v", err)
	}
}
