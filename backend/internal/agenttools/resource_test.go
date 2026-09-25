package agenttools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"easygpa/backend/internal/agentcontext"
	"easygpa/backend/internal/llm"
)

func evidenceExecutor(t *testing.T) (*Executor, agentcontext.Snapshot, string) {
	t.Helper()
	snapshot := agentcontext.Snapshot{Context: agentcontext.Context{View: "admSubs", ResourceKind: "submission", ResourceID: "1", Revision: "frozen"}, Evidence: []agentcontext.Evidence{{ID: "9", Filename: "proof.png", Status: "ready", SizeBytes: 100}}}
	e := &Executor{maxBytes: 32 << 10, handles: map[string]*Source{}, newHandle: randomHandle}
	e.WithResource(snapshot.Context, nil)
	handle := e.register(e.businessSource(snapshot, "evidence-9", "proof.png", "", false))
	return e, snapshot, handle
}

func TestEvidenceHandlesAreValidatedBeforeAnyRead(t *testing.T) {
	e, snapshot, handle := evidenceExecutor(t)
	calls := 0
	e.readEvidenceFile = func(context.Context, agentcontext.Evidence) (EvidenceContent, error) {
		calls++
		return EvidenceContent{Text: "secret"}, nil
	}
	if _, err := e.readEvidenceSnapshot(t.Context(), snapshot, []string{handle, "https://foreign.test/file"}); err == nil {
		t.Fatal("accepted an arbitrary location")
	}
	if calls != 0 {
		t.Fatal("part of an invalid batch was read")
	}
	if _, ok := e.ResolvedCitation(handle); ok {
		t.Fatal("unread original became citable")
	}
}

func TestOriginalImageIsDeliveredOncePerMessage(t *testing.T) {
	e, snapshot, handle := evidenceExecutor(t)
	calls := 0
	e.readEvidenceFile = func(context.Context, agentcontext.Evidence) (EvidenceContent, error) {
		calls++
		return EvidenceContent{Images: []llm.Image{{MediaType: "image/png", Data: []byte("image")}}}, nil
	}
	first, err := e.readEvidenceSnapshot(t.Context(), snapshot, []string{handle})
	if err != nil || len(first.Images) != 1 {
		t.Fatalf("first image: %#v %v", first, err)
	}
	second, err := e.readEvidenceSnapshot(t.Context(), snapshot, []string{handle})
	if err != nil || len(second.Images) != 0 || calls != 1 || !strings.Contains(string(second.JSON), "alreadyInContext") {
		t.Fatalf("image was re-downloaded or duplicated: calls=%d %v", calls, err)
	}
	if _, ok := e.ResolvedCitation(handle); !ok {
		t.Fatal("read original cannot be cited")
	}
}

func TestEvidenceLimitsKeepUnreadFilesUncitable(t *testing.T) {
	for _, status := range []string{"pending", "ready"} {
		e, snapshot, handle := evidenceExecutor(t)
		snapshot.Evidence[0].Status = status
		snapshot.Evidence[0].SizeBytes = 13 << 20
		e.readEvidenceFile = func(context.Context, agentcontext.Evidence) (EvidenceContent, error) {
			t.Fatal("unready/oversized file was downloaded")
			return EvidenceContent{}, nil
		}
		result, err := e.readEvidenceSnapshot(t.Context(), snapshot, []string{handle})
		if err != nil || !strings.Contains(string(result.JSON), `"read":false`) {
			t.Fatalf("missing unread marker: %s %v", result.JSON, err)
		}
		if _, ok := e.ResolvedCitation(handle); ok {
			t.Fatal("skipped file became citable")
		}
	}
}

func TestConvertedEvidenceIsBoundedAndPartialIsExplicit(t *testing.T) {
	e, snapshot, handle := evidenceExecutor(t)
	e.maxBytes = 8192
	e.readEvidenceFile = func(context.Context, agentcontext.Evidence) (EvidenceContent, error) {
		return EvidenceContent{Text: strings.Repeat("中文原件<&>\n", 2000)}, nil
	}
	result, err := e.readEvidenceSnapshot(t.Context(), snapshot, []string{handle})
	if err != nil || len(result.JSON) > e.maxBytes || !json.Valid(result.JSON) || !utf8.Valid(result.JSON) {
		t.Fatalf("invalid bounded result, %d bytes: %v", len(result.JSON), err)
	}
	if !strings.Contains(string(result.JSON), "部分文本") {
		t.Fatal("truncated evidence was presented as complete")
	}
}
