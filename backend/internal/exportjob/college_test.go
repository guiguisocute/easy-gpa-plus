package exportjob

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"easygpa/backend/internal/scheme"
)

func unzipCollege(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string][]byte{}
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[file.Name] = content
	}
	return result
}

func collegeTestFiles(t *testing.T, data jobData) map[string][]byte {
	t.Helper()
	raw, err := collegeArchive(data)
	if err != nil {
		t.Fatal(err)
	}
	return unzipCollege(t, raw)
}

func collegeTestBook(t *testing.T, files map[string][]byte, index int) *excelize.File {
	t.Helper()
	book, err := excelize.OpenReader(bytes.NewReader(files[collegeFileNames[index]]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { book.Close() })
	return book
}

func collegeCell(t *testing.T, book *excelize.File, sheet, cell, want string) {
	t.Helper()
	got, err := book.GetCellValue(sheet, cell)
	if err != nil || got != want {
		t.Fatalf("%s!%s = %q (%v), want %q", sheet, cell, got, err, want)
	}
}

func collegeDocText(t *testing.T, raw []byte) string {
	t.Helper()
	parts := unzipCollege(t, raw)
	var words []string
	for name, content := range parts {
		if strings.Contains(name, "/media/") {
			t.Fatal("export retained the template example photo")
		}
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(content))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid OOXML in %s: %v", name, err)
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "t" && name == "word/document.xml" {
				var text string
				if err := decoder.DecodeElement(&text, &start); err != nil {
					t.Fatal(err)
				}
				words = append(words, text)
			}
		}
	}
	return strings.Join(words, "")
}

func TestCollegeArchiveMatchesOriginalFiles(t *testing.T) {
	files := collegeTestFiles(t, collegeFixture())
	if len(files) != 4 {
		t.Fatalf("files = %d, want 4", len(files))
	}
	for _, name := range collegeFileNames {
		if len(files[name]) == 0 {
			t.Fatalf("missing %s", name)
		}
	}
	text := collegeDocText(t, files[collegeFileNames[0]])
	for _, want := range []string{"学生综合素质考评评分登记表", "操行素质分", "学生综合素质评优结果申报名册", "身份证号码", "银行卡号", "辅导员签名", "示例学院", "24级计算机科学与技术2班"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Word missing %s", want)
		}
	}
	if strings.Index(text, "2024001") > strings.Index(text, "2024002") || strings.Index(text, "2024002") > strings.Index(text, "2024003") {
		t.Fatal("attachment 1 must be ordered by SID")
	}
	if strings.Count(text, "2024003") != 1 || strings.Count(text, "2024001") != 2 {
		t.Fatal("attachment 2 must contain only award/honor applicants")
	}
	public := collegeDocText(t, files[collegeFileNames[1]])
	for _, want := range []string{"2025—2026学年", "5个工作日", "学院电话", "实际公示照片", "三好学生（1人）"} {
		if !strings.Contains(public, want) {
			t.Fatalf("public notice missing %s", want)
		}
	}
	for _, sample := range []string{"2023xxxxxxxx", "360008888888888888", "6217888888888888888", "XXXX级", "2026年XX"} {
		if strings.Contains(text+public, sample) {
			t.Fatalf("sample retained: %s", sample)
		}
	}
	four := collegeTestBook(t, files, 2)
	if strings.Join(four.GetSheetList(), "|") != "Sheet1" {
		t.Fatal("attachment 4 sheet renamed")
	}
	collegeCell(t, four, "Sheet1", "A1", "2025—2026学年综合素质奖学金获得者信息采集表")
	collegeCell(t, four, "Sheet1", "B3", "2024")
	collegeCell(t, four, "Sheet1", "D3", "2024001")
	collegeCell(t, four, "Sheet1", "F4", "贰等")
	for _, cell := range []string{"G3", "H3", "I3", "J3", "A5"} {
		collegeCell(t, four, "Sheet1", cell, "")
	}
	note, _ := four.GetCellValue("Sheet1", "A22")
	if !strings.Contains(note, "表格格式不允许做任何修改") {
		t.Fatal("attachment 4 lost its notice")
	}
	five := collegeTestBook(t, files, 3)
	if strings.Join(five.GetSheetList(), "|") != "综合素质奖学金|三好学生" {
		t.Fatal("attachment 5 sheet names changed")
	}
	collegeCell(t, five, "综合素质奖学金", "J3", "是否获三好学生")
	collegeCell(t, five, "综合素质奖学金", "C4", "女")
	collegeCell(t, five, "综合素质奖学金", "C5", "男")
	collegeCell(t, five, "综合素质奖学金", "G4", "2025-2026学年")
	collegeCell(t, five, "综合素质奖学金", "A6", "")
	collegeCell(t, five, "三好学生", "R3", "身心素质分数")
	collegeCell(t, five, "三好学生", "A4", "2024001")
	collegeCell(t, five, "三好学生", "A5", "")
	for _, cell := range []string{"K4", "M4", "O4"} {
		value, _ := five.GetCellValue("综合素质奖学金", cell)
		if len(strings.Split(value, ".")) != 2 || len(strings.Split(value, ".")[1]) != 2 {
			t.Fatalf("score must display two decimals: %s=%s", cell, value)
		}
	}
	for _, cell := range []string{"D3", "H3", "J3"} {
		id, _ := four.GetCellStyle("Sheet1", cell)
		style, err := four.GetStyle(id)
		if err != nil || (style.NumFmt != 49 && (style.CustomNumFmt == nil || *style.CustomNumFmt != "@")) {
			t.Fatalf("%s must be text", cell)
		}
	}
}

func TestCollegeArchiveEscapesTextAndKeepsHonorOnlyStudents(t *testing.T) {
	data := collegeFixture()
	data.CollegeName = "学院<&> {{.Class}}"
	data.Rows[2].Honor = true
	data.Rows[2].Name = "测试<&>"
	data.Rows[0].Gender = ""
	files := collegeTestFiles(t, data)
	text := collegeDocText(t, files[collegeFileNames[0]])
	if strings.Count(text, "2024003") != 2 || !strings.Contains(text, data.CollegeName) || !strings.Contains(text, data.Rows[2].Name) {
		t.Fatal("escaped labels or honor-only applicant missing")
	}
	five := collegeTestBook(t, files, 3)
	collegeCell(t, five, "综合素质奖学金", "C4", "")
	collegeCell(t, five, "三好学生", "A5", "2024003")
	collegeCell(t, five, "三好学生", "I5", "")
}

func TestCollegeArchiveExpandsOriginalTemplatesWithoutLosingRows(t *testing.T) {
	data := collegeFixture()
	row := data.Rows[0]
	data.Rows = nil
	for i := 0; i < 105; i++ {
		r := row
		r.UserID = int64(i + 1)
		r.SID = fmt.Sprintf("202400%06d", i+1)
		r.Name = fmt.Sprintf("测试%03d", i+1)
		data.Rows = append(data.Rows, r)
	}
	files := collegeTestFiles(t, data)
	text := collegeDocText(t, files[collegeFileNames[0]])
	for _, row := range data.Rows {
		if strings.Count(text, row.SID) != 2 {
			t.Fatalf("missing/duplicated %s in attachment 1–2", row.SID)
		}
	}
	if !strings.Contains(text, "第 11 页、共 11 页") {
		t.Fatal("table pagination incorrect")
	}
	four := collegeTestBook(t, files, 2)
	collegeCell(t, four, "Sheet1", "D107", "202400000105")
	note, _ := four.GetCellValue("Sheet1", "A108")
	if !strings.HasPrefix(note, "注意事项") {
		t.Fatal("extra students overwrote the notice")
	}
	five := collegeTestBook(t, files, 3)
	collegeCell(t, five, "综合素质奖学金", "A108", "202400000105")
	collegeCell(t, five, "三好学生", "A108", "202400000105")
	public := collegeDocText(t, files[collegeFileNames[1]])
	if strings.Count(public, "测试105") != 2 {
		t.Fatal("public notice lost a name")
	}
}

func TestCollegeArchiveEmptyAwardsAndForcedWarning(t *testing.T) {
	data := collegeFixture()
	for i := range data.Rows {
		data.Rows[i].AwardTier = ""
		data.Rows[i].Honor = false
	}
	data.TriggerKind = "forced"
	data.GateSnapshot = map[string]any{"forceReason": "测试强制理由", "pendingConflicts": 1}
	files := collegeTestFiles(t, data)
	if !strings.Contains(string(files["强制结算警告.txt"]), "测试强制理由") {
		t.Fatal("forced warning missing")
	}
	public := collegeDocText(t, files[collegeFileNames[1]])
	if !strings.Contains(public, "三好学生（0人）") {
		t.Fatal("empty honor count incorrect")
	}
	four := collegeTestBook(t, files, 2)
	collegeCell(t, four, "Sheet1", "D3", "")
	five := collegeTestBook(t, files, 3)
	collegeCell(t, five, "综合素质奖学金", "A4", "")
	data.Config.Weights["custom"] = 0.1
	if _, err := collegeArchive(data); err == nil {
		t.Fatal("must not silently omit a scored custom category")
	}
}

// Optional synthetic artifacts for Office visual QA; no student data is read.
func TestCollegeRenderFixture(t *testing.T) {
	dir := os.Getenv("EASYGPA_COLLEGE_QA_DIR")
	if dir == "" {
		t.Skip("set EASYGPA_COLLEGE_QA_DIR for synthetic Office QA")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := collegeFixture()
	base := data.Rows[0]
	data.Rows = nil
	for i := 0; i < 32; i++ {
		row := base
		row.UserID = int64(i + 1)
		row.SID = fmt.Sprintf("202400%06d", i+1)
		row.Name = fmt.Sprintf("测试%02d", i+1)
		row.Honor = i < 8
		if i >= 10 {
			row.AwardTier = ""
		} else if i >= 5 {
			row.AwardTier = data.AwardOrder[2]
		} else if i >= 2 {
			row.AwardTier = data.AwardOrder[1]
		}
		data.Rows = append(data.Rows, row)
	}
	for name, raw := range collegeTestFiles(t, data) {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func collegeFixture() jobData {
	return jobData{
		ClassName:       "示例班级",
		CollegeName:     "示例学院",
		EnrollmentClass: "24级计算机科学与技术2班",
		AwardOrder:      []string{"一等综合素质奖学金", "二等综合素质奖学金", "三等综合素质奖学金"},
		AcademicYear:    "2025-2026",
		Config: scheme.Config{
			Weights: map[string]float64{"major": 0.6, "moral": 0.15, "practice": 0.15, "health": 0.1},
			Categories: []scheme.Category{
				{Key: "moral", Name: "思想道德素质"},
				{Key: "practice", Name: "实践创新素质"},
				{Key: "health", Name: "身体心理素质"},
			},
		},
		Rows: []snapshotRow{
			// 原始分带三位小数，逐项折算后必然出现进位残差。
			{UserID: 1, SID: "2024001", Name: "甲", Gender: "女", ClassRank: 1, Honor: true, AwardTier: "一等综合素质奖学金",
				CategoryScores: map[string]float64{"major": 97.755, "moral": 60.217, "practice": 60.883, "health": 90.005},
				TotalScore:     97.755*0.6 + 60.217*0.15 + 60.883*0.15 + 90.005*0.1},
			{UserID: 2, SID: "2024002", Name: "乙", Gender: "男", ClassRank: 2, AwardTier: "二等综合素质奖学金",
				CategoryScores: map[string]float64{"major": 80.111, "moral": 50.333, "practice": 40.777, "health": 70.999},
				TotalScore:     80.111*0.6 + 50.333*0.15 + 40.777*0.15 + 70.999*0.1},
			{UserID: 3, SID: "2024003", Name: "丙", ClassRank: 3,
				CategoryScores: map[string]float64{"major": 60, "moral": 40, "practice": 30, "health": 50},
				TotalScore:     60*0.6 + 40*0.15 + 30*0.15 + 50*0.1},
		},
	}
}

// 档位名按方案里的下标映射成壹/贰/叁，不按中文名匹配——名字是班级可配的。
func TestCollegeTierFollowsAwardOrderNotName(t *testing.T) {
	order := []string{"甲档奖学金", "乙档奖学金", "丙档奖学金"}
	for name, want := range map[string]string{"甲档奖学金": "壹等", "乙档奖学金": "贰等", "丙档奖学金": "叁等", "": ""} {
		if got := collegeTier(name, order); got != want {
			t.Fatalf("collegeTier(%q) = %q, want %q", name, got, want)
		}
	}
	// 不在档位表里的名字原样交出去，而不是硬塞进一个序数。
	if got := collegeTier("特设奖", order); got != "特设奖" {
		t.Fatalf("未知档位被改写成了 %q", got)
	}
}

func TestParseEnrollment(t *testing.T) {
	for input, want := range map[string][2]string{
		"24级计算机科学与技术2班": {"2024级", "计算机科学与技术"},
		"2024级人工智能11班":  {"2024级", "人工智能"},
		"24级软件工程班":      {"2024级", "软件工程"},
		"随便写的班名":        {"", ""},
	} {
		grade, major := parseEnrollment(input)
		if grade != want[0] || major != want[1] {
			t.Fatalf("parseEnrollment(%q) = %q/%q, want %q/%q", input, grade, major, want[0], want[1])
		}
	}
}
