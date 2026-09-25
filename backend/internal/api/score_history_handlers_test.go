package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStudentScoreHistoryReasonHidesEmbeddedFiles(t *testing.T) {
	for _, reason := range []string{
		"核对后认定三分 ![私密文件.png](evidence:123)",
		"核对后认定三分 [私密文件.pdf](https://example.test/private)",
		"核对后认定三分 ![私密[文件].png](evidence:123)",
		"核对后认定三分 ![私密文件.png][proof]\n[proof]: evidence:123",
		"核对后认定三分 <img src='https://example.test/private' alt='私密文件.png'>",
		"核对后认定三分 https://example.test/private evidence:123",
	} {
		got := studentScoreHistoryReason(reason)
		if !strings.Contains(got, "核对后认定三分") {
			t.Fatalf("lost the decision text: %q", got)
		}
		for _, secret := range []string{"私密", "evidence:", "example.test", "123"} {
			if strings.Contains(got, secret) {
				t.Fatalf("attachment detail %q exposed in %q", secret, got)
			}
		}
	}
}

func TestScoreHistoryEventDoesNotSerializeInternalSource(t *testing.T) {
	zero := 0.0
	author := int64(773366)
	event := scoreHistoryEvent{Kind: "force_reject", Score: &zero, source: "private-source", sourceID: 984321, authorID: &author}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-source") || strings.Contains(string(raw), "984321") || strings.Contains(string(raw), "773366") || !strings.Contains(string(raw), `"score":0`) {
		t.Fatalf("unexpected public projection: %s", raw)
	}
}
