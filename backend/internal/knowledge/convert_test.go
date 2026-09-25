package knowledge

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"

	"easygpa/backend/internal/llm"
)

type fakeVision struct {
	calls int
}

func (f *fakeVision) Call(_ context.Context, request llm.Request) (llm.Response, error) {
	f.calls++
	if request.Image == nil || request.Image.MediaType == "" || len(request.Image.Data) == 0 {
		return llm.Response{}, os.ErrInvalid
	}
	return llm.Response{Content: `{"text":"扫描页 OCR 内容"}`, Usage: llm.Usage{TotalTokens: 7}}, nil
}

func TestConvertTextGB18030AndLineNumbers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "规则.txt")
	raw, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("第一行\r\n第二行"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := (Converter{}).Convert(context.Background(), path, "规则.txt", "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" || !result.Searchable || len(result.Entries) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Entries[0].Text != "第一行\n第二行" || result.Entries[0].LineCount() != 2 {
		t.Fatalf("unexpected normalized text %q", result.Entries[0].Text)
	}
	if result.Entries[0].Metadata["encoding"] != "gb18030" {
		t.Fatalf("encoding = %#v", result.Entries[0].Metadata["encoding"])
	}
}

func TestConvertTextEnforcesExtractedOutputLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(path, []byte("123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (Converter{MaxExtractedBytes: 8}).Convert(context.Background(), path, "large.txt", "text/plain")
	if err == nil || !strings.Contains(err.Error(), "提取文本") {
		t.Fatalf("error = %v", err)
	}
}

func TestConvertDOCXExtractsParagraphsAndTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "规则.docx")
	writeZip(t, path, map[string][]byte{
		"[Content_Types].xml": []byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`),
		"word/document.xml":   []byte(`<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>干部任职</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>最高</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>不累计</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`),
	})
	result, err := (Converter{}).Convert(context.Background(), path, "规则.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || !strings.Contains(result.Entries[0].Text, "干部任职") || !strings.Contains(result.Entries[0].Text, "不累计") {
		t.Fatalf("DOCX text = %#v", result.Entries)
	}
}

func TestConvertDOCXUsesArchiveBombLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bomb.docx")
	writeZip(t, path, map[string][]byte{
		"[Content_Types].xml": []byte("<Types/>"),
		"word/document.xml":   bytes.Repeat([]byte("A"), 128*1024),
	})
	_, err := (Converter{}).Convert(context.Background(), path, "bomb.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	if err == nil || !strings.Contains(err.Error(), "压缩比异常") {
		t.Fatalf("error = %v", err)
	}
}

func TestConvertXLSXPreservesFormulaAndCoordinates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "速查表.xlsx")
	book := excelize.NewFile()
	if err := book.SetCellValue("Sheet1", "A1", "项目"); err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellFormula("Sheet1", "B2", "SUM(C2:D2)"); err != nil {
		t.Fatal(err)
	}
	if err := book.MergeCell("Sheet1", "A3", "B3"); err != nil {
		t.Fatal(err)
	}
	if err := book.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	_ = book.Close()

	result, err := (Converter{}).Convert(context.Background(), path, "速查表.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	if err != nil {
		t.Fatal(err)
	}
	if result.SheetCount != 1 || len(result.Entries) != 1 {
		t.Fatalf("unexpected result %#v", result)
	}
	entry := result.Entries[0]
	if !strings.Contains(entry.Text, "A1=项目") || !strings.Contains(entry.Text, "B2= [formula=SUM(C2:D2)]") {
		t.Fatalf("sheet text = %q", entry.Text)
	}
	formulas, ok := entry.Metadata["formulas"].(map[string]string)
	if !ok || formulas["B2"] != "SUM(C2:D2)" {
		t.Fatalf("formulas = %#v", entry.Metadata["formulas"])
	}
}

func TestConvertZIPRejectsTraversalAndBombRatio(t *testing.T) {
	tests := []struct {
		name string
		path string
		data []byte
		want string
	}{
		{name: "traversal", path: "../secret.txt", data: []byte("secret"), want: "路径穿越"},
		{name: "ratio", path: "large.txt", data: bytes.Repeat([]byte("A"), 1024*1024), want: "压缩比异常"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "upload.zip")
			writeZip(t, path, map[string][]byte{test.path: test.data})
			_, err := (Converter{}).Convert(context.Background(), path, "upload.zip", "application/zip")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConvertZIPDoesNotExpandNestedArchive(t *testing.T) {
	var nested bytes.Buffer
	nestedWriter := zip.NewWriter(&nested)
	member, err := nestedWriter.Create("inner.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = member.Write([]byte("inner secret"))
	if err := nestedWriter.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "upload.zip")
	writeZip(t, path, map[string][]byte{"nested.zip": nested.Bytes(), "readme.txt": []byte("visible")})
	result, err := (Converter{}).Convert(context.Background(), path, "upload.zip", "application/zip")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partial" || len(result.Entries) != 2 {
		t.Fatalf("unexpected nested archive result %#v", result)
	}
	if strings.Contains(result.Entries[0].Text, "inner secret") {
		t.Fatal("nested archive content must not be expanded")
	}
}

func TestCheckedArchiveMemberSizeRejectsIntegerOverflow(t *testing.T) {
	if _, err := checkedArchiveMemberSize(^uint64(0), 0, MaxArchiveBytes); err == nil {
		t.Fatal("overflowing ZIP member size was accepted")
	}
	if _, err := checkedArchiveMemberSize(uint64(MaxArchiveBytes), 1, MaxArchiveBytes); err == nil {
		t.Fatal("ZIP member exceeding the remaining budget was accepted")
	}
}

func TestDetectMediaTypeRejectsFakePDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.pdf")
	if err := os.WriteFile(path, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := DetectMediaType(path, "fake.pdf", "application/pdf")
	if err == nil || !strings.Contains(err.Error(), "实际内容不一致") {
		t.Fatalf("error = %v", err)
	}
}

func TestConvertPDFTextAndOCRFallback(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext is not installed in this host test environment")
	}
	dir := t.TempDir()
	textPDF := filepath.Join(dir, "text.pdf")
	writeMinimalPDF(t, textPDF, "EasyGPA Plus official rule page")
	textResult, err := (Converter{}).Convert(context.Background(), textPDF, "text.pdf", "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if textResult.PageCount != 1 || !strings.Contains(textResult.Entries[0].Text, "official rule") {
		t.Fatalf("text PDF = %#v", textResult)
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm is not installed in this host test environment")
	}

	blankPDF := filepath.Join(dir, "scan.pdf")
	writeMinimalPDF(t, blankPDF, "")
	vision := &fakeVision{}
	scanResult, err := (Converter{AllowOCR: true, Vision: vision, VisionModel: "vision-test"}).Convert(context.Background(), blankPDF, "scan.pdf", "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if vision.calls != 1 || !strings.Contains(scanResult.Entries[0].Text, "OCR") || scanResult.Entries[0].Metadata["ocr"] != "vision" {
		t.Fatalf("scan PDF = %#v, calls=%d", scanResult, vision.calls)
	}
}

func TestNativeToolKillSwitchSkipsPDFExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.pdf")
	writeMinimalPDF(t, path, "content")
	result, err := (Converter{DisableNativeTools: true}).Convert(context.Background(), path, "sample.pdf", "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unsupported" || !strings.Contains(result.Warning, "运维关闭") {
		t.Fatalf("result = %#v", result)
	}
}

func writeZip(t *testing.T, path string, members map[string][]byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, data := range members {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalPDF(t *testing.T, path, text string) {
	t.Helper()
	stream := "BT /F1 12 Tf 72 720 Td (" + strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(text) + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Length " + stringInt(len(stream)) + " >>\nstream\n" + stream + "\nendstream",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = out.Len()
		out.WriteString(stringInt(index+1) + " 0 obj\n" + object + "\nendobj\n")
	}
	xref := out.Len()
	out.WriteString("xref\n0 " + stringInt(len(objects)+1) + "\n0000000000 65535 f \n")
	for index := 1; index <= len(objects); index++ {
		out.WriteString(leftPad10(offsets[index]) + " 00000 n \n")
	}
	out.WriteString("trailer\n<< /Size " + stringInt(len(objects)+1) + " /Root 1 0 R >>\nstartxref\n" + stringInt(xref) + "\n%%EOF\n")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stringInt(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var result [24]byte
	index := len(result)
	for value > 0 {
		index--
		result[index] = digits[value%10]
		value /= 10
	}
	return string(result[index:])
}

func leftPad10(value int) string {
	text := stringInt(value)
	return strings.Repeat("0", 10-len(text)) + text
}
