package agentcontext

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceLocatorRejectsForgedViewsAndIdentifiers(t *testing.T) {
	for _, input := range []Context{
		{View: "stuSubmit", ResourceKind: "submission", ResourceID: "1"},
		{View: "admSubs", ResourceKind: "sql", ResourceID: "1"},
		{View: "admSubs", ResourceKind: "submission", ResourceID: "01"},
		{View: "admSubs", ResourceKind: "submission", ResourceID: "-1"},
		{View: "admSubs", ResourceKind: "submission", ResourceID: "1", EvidenceID: "https://example.test"},
	} {
		if _, err := ResolveInput("class_admin", input); err == nil {
			t.Errorf("accepted invalid locator: %#v", input)
		}
	}
	got, err := ResolveInput("class_admin", Context{View: "admSubs", ResourceKind: "submission", ResourceID: "42", ActualRole: "ops", ResourceLabel: "forged"})
	if err != nil || got.ActualRole != "class_admin" || got.ResourceLabel != "" {
		t.Fatalf("trusted client claims: %#v, %v", got, err)
	}
	if _, err := ResolveInput("student", got); err == nil {
		t.Fatal("student gained adjudication access")
	}
}

func TestDeputyResourceBoundary(t *testing.T) {
	for _, tc := range []struct {
		role                        string
		deputy                      bool
		view, subject, kind, status string
		allowed                     bool
	}{
		{"group", true, "revDeputy", "class_admin", "submission", "arbitrating", true},
		{"group", true, "revDeputy", "student", "submission", "arbitrating", false},
		{"group", false, "revDeputy", "class_admin", "submission", "arbitrating", false},
		{"group", true, "admSubs", "class_admin", "submission", "arbitrating", false},
		{"class_admin", false, "revDeputy", "class_admin", "submission", "arbitrating", false},
		{"group", true, "revDeputy", "class_admin", "submission", "draft", false},
		{"group", true, "revDeputy", "class_admin", "appeal", "draft", false},
		{"group", true, "revDeputy", "class_admin", "appeal", "reviewing", true},
	} {
		if got := CanReadAdjudication(tc.role, tc.deputy, tc.view, tc.subject, tc.kind, tc.status); got != tc.allowed {
			t.Errorf("%+v: allowed=%v", tc, got)
		}
	}
}

func TestResourceRevisionAndPublicEvidence(t *testing.T) {
	s := Snapshot{Context: Context{View: "admSubs", ResourceKind: "submission", ResourceID: "7", Revision: "current"}, Evidence: []Evidence{{ID: "8", Filename: "proof.png", ObjectKey: "secret-private-object"}}}
	if s.CheckRevision("old") == nil || s.CheckRevision("current") != nil {
		t.Fatal("stale revision was accepted")
	}
	decoded, err := ParseDocumentID(s.Context.DocumentID())
	if err != nil || decoded.ResourceID != "7" || decoded.View != "admSubs" {
		t.Fatalf("locator round-trip: %#v %v", decoded, err)
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "secret-private-object") || strings.Contains(string(raw), "objectKey") {
		t.Fatal("public metadata disclosed the storage key")
	}
}

func TestObservedPageVersionMustMatchServer(t *testing.T) {
	s := Snapshot{Facts: json.RawMessage(`{"updatedAt":"2026-09-06T12:30:10.123456+00:00"}`)}
	if s.CheckObservedAt("2026-09-06T20:30:10.123456+08:00") != nil {
		t.Fatal("same instant with another timezone was rejected")
	}
	for _, value := range []string{"2026-09-06T12:30:09Z", "not-a-date"} {
		if s.CheckObservedAt(value) == nil {
			t.Fatalf("accepted old/invalid page version %q", value)
		}
	}
}
