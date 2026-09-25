package knowledge

import (
	"archive/zip"
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestConvertZIPDecodesLegacyNamesBeforeMetadataAndPathChecks(t *testing.T) {
	name := "材料/班级细则.rar"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.zip")
	writeZip(t, path, map[string][]byte{string(encoded): []byte("nested archive is metadata only")})
	result, err := (Converter{}).Convert(context.Background(), path, "legacy.zip", "application/zip")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial" || len(result.Entries) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	entry := result.Entries[0]
	if entry.Locator["member"] != name || entry.Text != "文件名: "+name || !utf8.ValidString(entry.Text) {
		t.Fatalf("legacy member was not decoded: %#v", entry)
	}

	// 0x5c is also a valid trailing byte inside GBK/GB18030 characters. It must
	// not turn into a directory separator before the whole name is decoded.
	legacyName := []byte{0x81, 0x5c}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(legacyName)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "trail-byte.zip")
	writeZip(t, path, map[string][]byte{string(legacyName) + ".txt": []byte("visible text")})
	result, err = (Converter{}).Convert(context.Background(), path, "trail-byte.zip", "application/zip")
	if err != nil {
		t.Fatal(err)
	}
	if result.Entries[0].Locator["member"] != string(decoded)+".txt" {
		t.Fatalf("multibyte filename was split: %#v", result.Entries[0].Locator)
	}
}

func TestConvertZIPRejectsLegacyTraversalAndInvalidNames(t *testing.T) {
	legacyTraversal, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("../材料.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{string(legacyTraversal), string([]byte{0xb5}) + ".txt", "name\x00.txt", "C:\\data.txt"} {
		path := filepath.Join(t.TempDir(), "invalid.zip")
		writeZip(t, path, map[string][]byte{name: []byte("data")})
		if _, err := (Converter{}).Convert(context.Background(), path, "invalid.zip", "application/zip"); err == nil {
			t.Fatalf("unsafe or invalid filename was accepted: %q", name)
		}
	}
}

func TestDecodeArchiveNameRejectsInvalidUTF8Declaration(t *testing.T) {
	legacy, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("材料.txt"))
	if err != nil {
		t.Fatal(err)
	}
	member := &zip.File{FileHeader: zip.FileHeader{Name: string(legacy), Flags: 1 << 11}}
	if _, err := decodeArchiveName(member); err == nil {
		t.Fatal("invalid UTF-8 declaration must not be silently reinterpreted")
	}
}

func TestDecodeCommandOutputPreservesTextAndRejectsMalformedBytes(t *testing.T) {
	want := []byte("转换结果\r\n第二行")
	legacy, err := simplifiedchinese.GB18030.NewEncoder().Bytes(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{want, legacy} {
		got, err := decodeCommandOutput(raw, 1024)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("output = %q, error = %v", got, err)
		}
	}
	for _, raw := range [][]byte{{0xb5}, {0xef, 0xbb, 0xbf, 0xb5}} {
		if _, err := decodeCommandOutput(raw, 1024); err == nil {
			t.Fatal("malformed tool output must not be replaced silently")
		}
	}
	if _, err := decodeCommandOutput(legacy, int64(len(legacy))); err == nil {
		t.Fatal("decoded UTF-8 must still obey the output size limit")
	}
}

func TestConversionResultValidatesTextAndMetadataBeforePersistence(t *testing.T) {
	for _, entry := range []Entry{
		{Text: "bad\xb5"},
		{Text: "valid", Locator: map[string]any{"member": "bad\xb5"}},
		{Text: "valid", Metadata: map[string]any{"nested": map[string]string{"field": "bad\x00"}}},
	} {
		result := Result{Status: "ready", Entries: []Entry{entry}}
		if err := result.ValidateText(); err == nil || !strings.Contains(err.Error(), "第 1 项") {
			t.Fatalf("invalid conversion result accepted: %#v, error = %v", result, err)
		}
	}
}
