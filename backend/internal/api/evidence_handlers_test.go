package api

import (
	"strings"
	"testing"
	"unicode/utf8"

	"easygpa/backend/internal/opsconfig"
	"easygpa/backend/internal/scheme"
)

// 超长中文名按字节切会切出半个汉字。文件名会进 Content-Disposition 和归档包，
// 非法 UTF-8 在那两处才暴露的话，回头很难定位到这里。
func TestSafeFilenameTruncatesChineseAtUTF8Boundary(t *testing.T) {
	filename := safeFilename(strings.Repeat("规则", 100) + ".pdf")
	if !utf8.ValidString(filename) {
		t.Fatalf("truncated filename is not valid UTF-8: %q", filename)
	}
	if !strings.HasSuffix(filename, ".pdf") {
		t.Fatalf("extension was not preserved: %q", filename)
	}
}

func TestValidateEvidenceUsesLowerPlatformLimit(t *testing.T) {
	rule := &scheme.EvidenceRule{Types: []string{"pdf"}, MaxMB: 50}
	input := evidenceInput{Filename: "proof.pdf", MediaType: "application/pdf", SizeBytes: 21 * 1024 * 1024}
	if err := validateEvidence(rule, input, 20); err == nil {
		t.Fatal("expected platform upload limit to reject the file")
	}
	input.SizeBytes = 20 * 1024 * 1024
	if err := validateEvidence(rule, input, 20); err != nil {
		t.Fatalf("20 MB file rejected: %v", err)
	}
}

func TestValidateSchemeEvidenceLimit(t *testing.T) {
	cfg := scheme.Config{Categories: []scheme.Category{{Items: []scheme.Item{{
		Key: "award", Evidence: &scheme.EvidenceRule{Types: []string{"pdf"}, MaxMB: 25},
	}}}}}
	if err := validateSchemeEvidenceLimit(cfg, 20); err == nil {
		t.Fatal("expected scheme evidence limit validation to fail")
	}
}

func TestValidateEvidenceKeepsLegacyLookingAllowListStrict(t *testing.T) {
	rule := &scheme.EvidenceRule{Types: []string{"pdf", "jpg", "png"}, MaxMB: 10}
	input := evidenceInput{
		Filename:  "competition-proof.docx",
		MediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		SizeBytes: 1024,
	}
	if err := validateEvidence(rule, input, 20); err == nil {
		t.Fatal("explicit PDF/JPG/PNG allow-list unexpectedly accepted a DOCX file")
	}
}

func TestValidateEvidenceAcceptsMP4WhenConfigured(t *testing.T) {
	rule := &scheme.EvidenceRule{Types: []string{"mp4"}, MaxMB: 50}
	input := evidenceInput{Filename: "constitution-day.mp4", MediaType: "video/mp4", SizeBytes: 9 * 1024 * 1024}
	if err := validateEvidence(rule, input, 50); err != nil {
		t.Fatalf("configured MP4 was rejected: %v", err)
	}
}

func TestValidateEvidenceUsesFiftyMBDefault(t *testing.T) {
	rule := &scheme.EvidenceRule{Types: []string{"pdf"}, MaxMB: 50}
	input := evidenceInput{Filename: "scanned-bundle.pdf", MediaType: "application/pdf", SizeBytes: 48 * 1024 * 1024}
	if err := validateEvidence(rule, input, 0); err != nil {
		t.Fatalf("48 MB PDF was rejected by the platform default: %v", err)
	}
}

func TestValidateEvidenceKeepsCustomAllowListStrict(t *testing.T) {
	rule := &scheme.EvidenceRule{Types: []string{"pdf"}, MaxMB: 10}
	input := evidenceInput{
		Filename:  "competition-proof.docx",
		MediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		SizeBytes: 1024,
	}
	if err := validateEvidence(rule, input, 20); err == nil {
		t.Fatal("custom PDF-only rule unexpectedly accepted a DOCX file")
	}
}

func TestValidateEvidenceChecksOfficeMediaType(t *testing.T) {
	rule := &scheme.EvidenceRule{Types: []string{"docx"}, MaxMB: 10}
	input := evidenceInput{Filename: "proof.docx", MediaType: "image/png", SizeBytes: 1024}
	if err := validateEvidence(rule, input, 20); err == nil {
		t.Fatal("DOCX extension with image MIME type should be rejected")
	}
}

// 对象键必须是纯 ASCII。garage 解析 multipart 里的 key 字段时按 str 处理，
// 键里带中文一律回 400 InvalidHeaderValue —— 而微信导出的图片默认就叫
// 「微信图片_2025….jpg」，是学生最常见的来源，这条断言掉了等于这批文件又传不上。
// 微信和 QQ 会把图片重新编码成 WebP 或 HEIC，文件名却还是 .jpg。生产上 9 位同学
// 因此反复上传失败，所以这些组合必须是"照真实格式收下"，而不是把人挡回去。
func TestResolveEvidenceContentAcceptsMislabeledImages(t *testing.T) {
	platform := opsconfig.DefaultEvidenceAllowedFormats()
	cases := []struct {
		name      string
		filename  string
		mediaType string
		header    []byte
		wantName  string
		wantMedia string
	}{
		{"WebP 顶着 .jpg", "26ICPC南昌邀请铜.jpg", "image/jpeg", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "26ICPC南昌邀请铜.webp", "image/webp"},
		{"HEIC 顶着 .jpg", "mmexport1788707080092.jpg", "image/jpeg", []byte("\x00\x00\x00\x18ftypheic"), "mmexport1788707080092.heic", "image/heic"},
		{"PNG 顶着 .jpg", "海报设计大赛.jpg", "image/jpeg", []byte("\x89PNG\r\n\x1a\n"), "海报设计大赛.png", "image/png"},
		{"GIF 顶着 .png", "颁奖.png", "image/png", []byte("GIF89a\x00\x00"), "颁奖.gif", "image/gif"},
		{"PDF 顶着 .doc", "证明.doc", "application/msword", []byte("%PDF-1.7"), "证明.pdf", "application/pdf"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := resolveEvidenceContent(testCase.filename, testCase.mediaType, testCase.header, platform)
			if err != nil {
				t.Fatalf("按真实格式收下失败：%v", err)
			}
			if !result.Corrected || result.Filename != testCase.wantName || result.MediaType != testCase.wantMedia {
				t.Fatalf("结果是 %+v，期望 %q / %q", result, testCase.wantName, testCase.wantMedia)
			}
		})
	}
}

func TestResolveEvidenceContentLeavesMatchingFilesAlone(t *testing.T) {
	platform := opsconfig.DefaultEvidenceAllowedFormats()
	cases := []struct {
		name      string
		filename  string
		mediaType string
		header    []byte
	}{
		{"名副其实的 JPEG", "证书.jpg", "image/jpeg", []byte("\xff\xd8\xff\xe0")},
		{"名副其实的 PDF", "证明.pdf", "application/pdf", []byte("%PDF-1.4")},
		{"docx 就是个 ZIP", "汇总表.docx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []byte("PK\x03\x04\x14\x00")},
		{"老 xls 是 OLE", "名单.xls", "application/vnd.ms-excel", []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")},
		// GBK 和 UTF-16 都不是合法 UTF-8，旧判断会把它们当成"内容不对"。
		{"GBK 的 csv", "名单.csv", "text/csv", []byte("\xd1\xa7\xba\xc5,\xd0\xd5\xc3\xfb\r\n")},
		{"Excel 另存的 UTF-16 文本", "名单.txt", "text/plain", []byte("\xff\xfe\x66\x00\x6f\x00")},
		// BMP 的魔数只有 "BM" 两个字节，这行文本不能被认成图片。
		{"以 BM 开头的纯文本", "说明.txt", "text/plain", []byte("BMW 车展志愿证明，共 8 小时")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := resolveEvidenceContent(testCase.filename, testCase.mediaType, testCase.header, platform)
			if err != nil {
				t.Fatalf("正常文件被拒了：%v", err)
			}
			if result.Corrected || result.Filename != testCase.filename || result.MediaType != testCase.mediaType {
				t.Fatalf("正常文件不该被改动，得到 %+v", result)
			}
		})
	}
}

func TestResolveEvidenceContentStillRejectsWhatItShould(t *testing.T) {
	platform := opsconfig.DefaultEvidenceAllowedFormats()
	cases := []struct {
		name     string
		filename string
		header   []byte
		wantSaid string
	}{
		// 能被浏览器内联打开的只有 PDF 和图片，它们都得对上文件头，网页伪装不成。
		{"网页顶着 .jpg", "成绩单.jpg", []byte("<!DOCTYPE html><html>"), "认不出"},
		{"Word 临时锁文件", "~$狼王争霸赛.docx", []byte("\x00\x01\x02\x03"), "认不出"},
		{"空文件", "空的.png", nil, "认不出"},
		// 一个文件头对应好几种扩展名时不替人猜，让人自己改对。
		{"OLE 顶着 .jpg", "名单.jpg", []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"), "改成"},
		// 认出来了，但平台压根不收这种格式。
		{"BMP 顶着 .jpg", "扫描件.jpg", []byte("BM\x36\x84\x03\x00\x00\x00\x00\x00\x36\x00\x00\x00"), "平台不收"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := resolveEvidenceContent(testCase.filename, "image/jpeg", testCase.header, platform)
			if err == nil {
				t.Fatal("这份内容不该被收下")
			}
			if !strings.Contains(err.Error(), testCase.wantSaid) {
				t.Fatalf("提示没说清楚，实际是：%v", err)
			}
		})
	}
}

// 运维把某种格式关掉是硬约束（比如为了省存储关掉 mp4）。改扩展名绕过去不行。
func TestResolveEvidenceContentHonoursPlatformAllowList(t *testing.T) {
	video := []byte("\x00\x00\x00\x20ftypisom")
	if _, err := resolveEvidenceContent("聚会.jpg", "image/jpeg", video, opsconfig.DefaultEvidenceAllowedFormats()); err != nil {
		t.Fatalf("默认允许 mp4 时视频应当按 .mp4 收下：%v", err)
	}
	if _, err := resolveEvidenceContent("聚会.jpg", "image/jpeg", video, []string{"jpg", "png", "pdf"}); err == nil {
		t.Fatal("平台关掉 mp4 之后，改名成 .jpg 的视频不该被收下")
	}
}

func TestObjectKeySuffixIsAlwaysASCII(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     string
	}{
		{"微信导出名", "微信图片_20250906102651.jpg", "-20250906102651.jpg"},
		{"纯中文名只剩扩展名", "获奖证书.pdf", ".pdf"},
		{"英文名原样保留", "certificate-2025_final.pdf", "-certificate-2025_final.pdf"},
		{"中英混排只留英文", "2025年-Award证书.png", "-2025-Award.png"},
		{"没有可用字符时退化成空", "证书", ""},
		{"空文件名", "", ""},
		{"路径分隔符不会漏进键里", "a/b/c.jpg", "-abc.jpg"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := objectKeySuffix(testCase.filename)
			if got != testCase.want {
				t.Fatalf("objectKeySuffix(%q) = %q，期望 %q", testCase.filename, got, testCase.want)
			}
			for _, r := range got {
				if r > 127 {
					t.Fatalf("结果里混进了非 ASCII 字符 %q", r)
				}
			}
		})
	}
}

func TestObjectKeySuffixStaysBoundedAndASCIIForAnyInput(t *testing.T) {
	long := strings.Repeat("张三的获奖证书abc123", 40)
	got := objectKeySuffix(long)
	if len(got) > 81 {
		t.Fatalf("长度 %d 超出上限", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("结果不是合法 UTF-8")
	}
	for _, r := range got {
		if r > 127 {
			t.Fatalf("非 ASCII 字符 %q", r)
		}
	}
}
