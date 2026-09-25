package platformknowledge

import (
	"strings"
	"testing"
)

func TestManifestDocumentsAreCompleteAndReadable(t *testing.T) {
	manifest, err := Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 10 {
		t.Fatalf("manifest contains %d documents, want 10", len(manifest))
	}
	seenOrders := make(map[int]bool)
	for _, item := range manifest {
		if seenOrders[item.Order] {
			t.Fatalf("duplicate sort order %d", item.Order)
		}
		seenOrders[item.Order] = true
		content, err := builtinFiles.ReadFile(item.File)
		if err != nil {
			t.Fatalf("read %s: %v", item.File, err)
		}
		if !strings.HasPrefix(string(content), "# ") || len([]rune(string(content))) < 120 {
			t.Fatalf("%s is not a substantive Markdown document", item.File)
		}
	}
}

func TestBuiltinFAQCoversSubmissionAndSafetyBoundaries(t *testing.T) {
	raw, err := builtinFiles.ReadFile("content/90-faq-troubleshooting.md")
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{"在哪里交材料", "怎么交材料", "上传失败", "Agent 不可用", "不能改变系统权限", "不能自动提交"} {
		if !strings.Contains(content, required) {
			t.Fatalf("FAQ missing %q", required)
		}
	}
}

func TestManifestKeepsClassSpecificFactsOutOfPlatformMetadata(t *testing.T) {
	manifest, err := Manifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range manifest {
		for _, role := range item.Roles {
			if role != "student" && role != "group" && role != "class_admin" {
				t.Fatalf("%s exposes unsupported role %q", item.Key, role)
			}
		}
		if len(item.Views) == 0 || len(item.Keywords) == 0 {
			t.Fatalf("%s must declare pages and keywords", item.Key)
		}
	}
}
