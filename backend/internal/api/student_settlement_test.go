package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestStudentSettlementDetailsProtectsStoredRecorderIdentity(t *testing.T) {
	raw := []byte(`{"categories":{"moral":{"total":68}},"items":[],"gpaMissing":false,"baseItems":[{"id":"81","category":"moral","itemKey":"ordinary","name":"Ordinary","kind":"base","fullScore":70,"score":69,"basis":"Verified adjustment","recorded":true,"recorder":"Private Reviewer","recorderSid":"private-sid","recorderId":"9001","appeals":[]},{"id":"82","kind":"penalty","fullScore":null,"score":-1,"basis":"Verified penalty","recorded":true,"recorder":"Private Reviewer","recorderSid":"private-sid","appeals":[{"reason":"Please check","status":"final"}]},{"kind":"base","score":70,"recorded":false}]}`)
	original := bytes.Clone(raw)
	got, err := studentSettlementDetails(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("Private Reviewer")) || bytes.Contains(got, []byte("private-sid")) || bytes.Contains(got, []byte("recorder")) {
		t.Fatal("student response exposes recorder identity")
	}
	var expected, actual map[string]any
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for _, value := range expected["baseItems"].([]any) {
		item := value.(map[string]any)
		delete(item, "recorder")
		delete(item, "recorderSid")
		delete(item, "recorderId")
	}
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("projection changed public scores, basis, appeals or other details")
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("projection mutated the audit snapshot")
	}
}

func TestStudentSettlementDetailsLegacyAndMalformed(t *testing.T) {
	for _, raw := range []string{`{}`, `{"baseItems":[]}`, `{"baseItems":null}`} {
		if _, err := studentSettlementDetails([]byte(raw)); err != nil {
			t.Fatalf("legacy snapshot %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{`, `{"baseItems":{}}`, `{"baseItems":[1]}`} {
		if _, err := studentSettlementDetails([]byte(raw)); err == nil {
			t.Fatal("malformed snapshot must fail closed, never return unfiltered data")
		}
	}
}
