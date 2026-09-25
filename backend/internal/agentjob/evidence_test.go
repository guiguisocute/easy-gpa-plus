package agentjob

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"easygpa/backend/internal/agentcontext"
)

func TestEvidenceReaderRetainsOversizedTextPrefix(t *testing.T) {
	data := []byte("original-prefix:" + strings.Repeat("原始材料", 3000))
	w := &Worker{objects: &conversionObjects{source: data}}
	result, err := w.evidenceReader(Runtime{}, nil)(context.Background(), agentcontext.Evidence{
		Filename: "evidence.txt", MediaType: "text/plain", SizeBytes: int64(len(data)),
	})
	if err != nil || !strings.HasPrefix(result.Text, "original-prefix:") || len(result.Text) > 12000 || !utf8.ValidString(result.Text) || !strings.Contains(result.Warning, "后文未读") {
		t.Fatalf("oversized original must keep a valid, explicitly partial prefix: bytes=%d warning=%q err=%v", len(result.Text), result.Warning, err)
	}
}

func TestEvidenceReaderDoesNotExpandRenamedArchive(t *testing.T) {
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	entry, err := archive.Create("evidence.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("must not be read")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	w := &Worker{objects: &conversionObjects{source: data.Bytes()}}
	result, err := w.evidenceReader(Runtime{}, nil)(context.Background(), agentcontext.Evidence{
		Filename: "evidence.bin", MediaType: "application/octet-stream", SizeBytes: int64(data.Len()),
	})
	if err != nil || result.Text != "" || len(result.Images) != 0 || !strings.Contains(result.Warning, "压缩包未展开") {
		t.Fatalf("renamed archive was not safely skipped: %#v, %v", result, err)
	}
}
