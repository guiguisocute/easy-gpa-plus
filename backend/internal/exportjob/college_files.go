package exportjob

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/xuri/excelize/v2"
)

// These are sanitized school templates, not student source documents. Only
// document.xml in the DOCX templates contains Go template expressions.
//
//go:embed templates/*.docx.tmpl templates/*.xlsx
var collegeTemplates embed.FS

// CollegeArchiveSuffix identifies reports using independent decimal half-up rounding.
// New requests must not reuse archives generated under an older score policy.
const CollegeArchiveSuffix = "-college-round2-v2.zip"

var collegeFileNames = []string{
	"附件1-2.docx",
	"附件3：综合素质奖学金和“三好学生”名单公示.docx",
	"附件4：综合素质奖学金获得者信息采集表.xlsx",
	"附件5：综合素质奖学金、“三好学生”证书套打信息录入模板.xlsx",
}

type collegePage struct {
	Rows          [][]string
	Number, Total int
	BreakBefore   bool
}

type collegeAward struct {
	Name     string
	Count    int
	NameRows [][]string
}

type collegeDocumentData struct {
	College, Class, Major, Year, Date string
	AwardSummary, PublicSummary       string
	Count, HonorCount                 int
	OnePages, TwoPages                []collegePage
	Awards                            []collegeAward
	HonorRows                         [][]string
}

// Keep the two page patterns from attachment 1–2, including sign-off areas.
// Ten body rows fit the landscape page without orphaning its signature/footer.
func collegePages(rows [][]string, columns int, breakFirst bool) []collegePage {
	const perPage = 10
	count := max(1, (len(rows)+perPage-1)/perPage)
	pages := make([]collegePage, count)
	for i := range pages {
		body := make([][]string, 0, perPage)
		for j := 0; j < perPage; j++ {
			if index := i*perPage + j; index < len(rows) {
				body = append(body, rows[index])
			} else {
				body = append(body, make([]string, columns))
			}
		}
		pages[i] = collegePage{Rows: body, Number: i + 1, Total: count, BreakBefore: i > 0 || breakFirst}
	}
	return pages
}

func collegeNameRows(rows []collegeRow) [][]string {
	names := make([][]string, 0, (len(rows)+6)/7)
	for start := 0; start < len(rows); start += 7 {
		line := make([]string, 7)
		for j, row := range rows[start:min(start+7, len(rows))] {
			line[j] = row.Name
		}
		names = append(names, line)
	}
	return names
}

func collegeDocumentValues(data jobData, rows []collegeRow) collegeDocumentData {
	_, major := parseEnrollment(data.EnrollmentClass)
	year := data.AcademicYear
	if year == "" {
		year = "____-____"
	}
	values := collegeDocumentData{
		College: data.CollegeName, Class: data.EnrollmentClass, Major: major,
		Year:  strings.ReplaceAll(year, "-", "—") + "学年",
		Date:  time.Now().In(chinaZone).Format("2006年1月2日"),
		Count: len(rows), HonorCount: len(honored(rows)), HonorRows: collegeNameRows(honored(rows)),
	}
	bySID := append([]collegeRow(nil), rows...)
	sort.SliceStable(bySID, func(i, j int) bool { return bySID[i].SID < bySID[j].SID })
	var one, two [][]string
	for _, row := range bySID {
		cells := []string{row.SID, row.Name}
		for _, key := range []string{"major", "moral", "practice", "health"} {
			cells = append(cells, fmt.Sprintf("%.2f", row.Weighted[key]), strconv.Itoa(row.Ranks[key]))
		}
		one = append(one, append(cells, fmt.Sprintf("%.2f", row.Total), strconv.Itoa(row.ClassRank), ""))
	}
	applicants := append(awarded(rows), honorOnly(rows)...)
	for _, row := range applicants {
		two = append(two, []string{row.SID, row.Name, fmt.Sprintf("%.2f", row.Weighted["major"]), fmt.Sprintf("%.2f", row.Conduct), fmt.Sprintf("%.2f", row.Total), strconv.Itoa(row.ClassRank), row.Tier, boolText(row.Honor), "", "", ""})
	}
	values.OnePages = collegePages(one, 13, false)
	values.TwoPages = collegePages(two, 11, true)
	var counts []string
	for _, name := range data.AwardOrder {
		var members []collegeRow
		for _, row := range rows {
			if row.AwardTier == name {
				members = append(members, row)
			}
		}
		values.Awards = append(values.Awards, collegeAward{Name: name, Count: len(members), NameRows: collegeNameRows(members)})
		counts = append(counts, fmt.Sprintf("%s%d名", name, len(members)))
	}
	values.AwardSummary = "综合素质奖学金人数：" + strings.Join(counts, "；") + fmt.Sprintf("；三好学生%d名", values.HonorCount)
	values.PublicSummary = "评选出" + strings.Join(append(counts, fmt.Sprintf("“三好学生”%d名", values.HonorCount)), "、") + "。"
	return values
}

func honorOnly(rows []collegeRow) []collegeRow {
	var result []collegeRow
	for _, row := range rows {
		if row.Honor && row.Tier == "" {
			result = append(result, row)
		}
	}
	return result
}

func collegeDocx(name string, data collegeDocumentData) ([]byte, error) {
	raw, err := collegeTemplates.ReadFile("templates/" + name + ".docx.tmpl")
	if err != nil {
		return nil, err
	}
	source, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, part := range source.File {
		reader, err := part.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, err
		}
		if part.Name == "word/document.xml" {
			t, err := template.New(name).Funcs(template.FuncMap{"xml": func(s string) (string, error) {
				var escaped bytes.Buffer
				err := xml.EscapeText(&escaped, []byte(s))
				return escaped.String(), err
			}}).Option("missingkey=error").Parse(string(content))
			if err != nil {
				return nil, err
			}
			var rendered bytes.Buffer
			if err := t.Execute(&rendered, data); err != nil {
				return nil, err
			}
			content = rendered.Bytes()
		}
		dst, err := writer.Create(part.Name)
		if err != nil {
			return nil, err
		}
		if _, err = dst.Write(content); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// fillCollegeSheet changes only body values/styles. Headers, merged cells,
// notices, column widths, sheet names and printing setup belong to the school.
func fillCollegeSheet(book *excelize.File, sheet string, start, capacity, columns int, rows [][]any, textCols, scoreCols []int) error {
	for extra := len(rows) - capacity; extra > 0; extra-- {
		if err := book.DuplicateRowTo(sheet, start+1, start+capacity); err != nil {
			return err
		}
	}
	styles := make([]int, columns)
	widths := make([]float64, columns)
	fontSizes := make([]float64, columns)
	for col := 1; col <= columns; col++ {
		letter, _ := excelize.ColumnNumberToName(col)
		var err error
		widths[col-1], err = book.GetColWidth(sheet, letter)
		if err != nil {
			return err
		}
		cell, _ := excelize.CoordinatesToCellName(col, start+1)
		id, err := book.GetCellStyle(sheet, cell)
		if err != nil {
			return err
		}
		style, err := book.GetStyle(id)
		if err != nil {
			return err
		}
		if style.Font != nil {
			style.Font.Color = "000000"
			style.Font.ColorTheme = nil
			style.Font.ColorIndexed = 0
			fontSizes[col-1] = style.Font.Size
		}
		if fontSizes[col-1] == 0 {
			fontSizes[col-1] = 11
		}
		for _, c := range textCols {
			if c == col {
				f := "@"
				style.CustomNumFmt = &f
			}
		}
		for _, c := range scoreCols {
			if c == col {
				f := "0.00"
				style.CustomNumFmt = &f
			}
		}
		styles[col-1], err = book.NewStyle(style)
		if err != nil {
			return err
		}
	}
	for index := 0; index < max(capacity, len(rows)); index++ {
		for col := 1; col <= columns; col++ {
			cell, _ := excelize.CoordinatesToCellName(col, start+index)
			if err := book.SetCellStyle(sheet, cell, cell, styles[col-1]); err != nil {
				return err
			}
		}
		if index < len(rows) {
			if err := setRow(book, sheet, start+index, rows[index]); err != nil {
				return err
			}
			// Keep the school's column widths. Increase only occupied row heights
			// when wrapped college/major/class labels need more than a sample row.
			height, err := book.GetRowHeight(sheet, start+index)
			if err != nil {
				return err
			}
			for col, value := range rows[index] {
				units := 0.0
				for _, char := range fmt.Sprint(value) {
					if char > 127 {
						units += 2
					} else {
						units++
					}
				}
				lines := max(1, math.Ceil(units/max(1, widths[col]-2)))
				height = max(height, lines*fontSizes[col]*1.35+4)
			}
			if err := book.SetRowHeight(sheet, start+index, height); err != nil {
				return err
			}
		}
	}
	return nil
}

func collegeExcel(data jobData, rows []collegeRow, attachment int) ([]byte, error) {
	raw, err := collegeTemplates.ReadFile(fmt.Sprintf("templates/college-%d.xlsx", attachment))
	if err != nil {
		return nil, err
	}
	book, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer book.Close()
	grade, major := parseEnrollment(data.EnrollmentClass)
	year := data.AcademicYear
	if year != "" {
		year += "学年"
	}
	titleYear := strings.ReplaceAll(year, "-", "—")
	if titleYear == "" {
		titleYear = "____—____学年"
	}
	if attachment == 4 {
		var body [][]any
		for _, row := range awarded(rows) {
			body = append(body, []any{data.CollegeName, strings.TrimSuffix(grade, "级"), major, row.SID, row.Name, row.Tier, "", "", "", ""})
		}
		if err := book.SetCellValue("Sheet1", "A1", titleYear+"综合素质奖学金获得者信息采集表"); err != nil {
			return nil, err
		}
		if err := fillCollegeSheet(book, "Sheet1", 3, 19, 10, body, []int{4, 8, 10}, nil); err != nil {
			return nil, err
		}
	} else {
		identity := func(row collegeRow) []any {
			return []any{row.SID, row.Name, row.Gender, grade, data.CollegeName, major, year, data.EnrollmentClass}
		}
		var scholarships, honors [][]any
		for _, row := range awarded(rows) {
			scholarships = append(scholarships, append(identity(row), row.Tier, boolText(row.Honor), row.Total, row.ClassRank, row.Weighted["major"], row.Ranks["major"], row.Conduct, row.Ranks["conduct"]))
		}
		for _, row := range honored(rows) {
			cells := append(identity(row), row.Tier, row.Total, row.ClassRank)
			for _, key := range []string{"major", "moral", "practice", "health"} {
				cells = append(cells, row.Weighted[key], row.Ranks[key])
			}
			honors = append(honors, cells)
		}
		for sheet, title := range map[string]string{"综合素质奖学金": titleYear + "综合素质奖学金证书套打信息录入模板", "三好学生": titleYear + "“三好学生”证书套打信息录入模板"} {
			if err := book.SetCellValue(sheet, "A1", title); err != nil {
				return nil, err
			}
		}
		if err := fillCollegeSheet(book, "综合素质奖学金", 4, 99, 16, scholarships, []int{1}, []int{11, 13, 15}); err != nil {
			return nil, err
		}
		if err := fillCollegeSheet(book, "三好学生", 4, 99, 19, honors, []int{1}, []int{10, 12, 14, 16, 18}); err != nil {
			return nil, err
		}
	}
	var result bytes.Buffer
	if err := book.Write(&result); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func collegeArchive(data jobData) ([]byte, error) {
	for _, key := range []string{"major", "moral", "practice", "health"} {
		if _, ok := data.Config.Weights[key]; !ok {
			return nil, fmt.Errorf("学院附件要求专业、思想道德、实践创新、身体心理四大项，请核对班级方案")
		}
	}
	for key, weight := range data.Config.Weights {
		if weight != 0 && key != "major" && key != "moral" && key != "practice" && key != "health" {
			return nil, fmt.Errorf("学院附件不支持额外的计分大项，请核对班级方案")
		}
	}
	rows := collegeRows(data)
	values := collegeDocumentValues(data, rows)
	builders := []func() ([]byte, error){
		func() ([]byte, error) { return collegeDocx("college-12", values) },
		func() ([]byte, error) { return collegeDocx("college-3", values) },
		func() ([]byte, error) { return collegeExcel(data, rows, 4) },
		func() ([]byte, error) { return collegeExcel(data, rows, 5) },
	}
	var result bytes.Buffer
	writer := zip.NewWriter(&result)
	for i, build := range builders {
		raw, err := build()
		if err != nil {
			return nil, fmt.Errorf("%s：%w", collegeFileNames[i], err)
		}
		file, err := writer.Create(collegeFileNames[i])
		if err != nil {
			return nil, err
		}
		if _, err = file.Write(raw); err != nil {
			return nil, err
		}
	}
	// Preserve the existing forced-settlement warning without adding a worksheet
	// to the school's fixed-format certificate import templates.
	if data.TriggerKind == "forced" {
		file, err := writer.Create("强制结算警告.txt")
		if err != nil {
			return nil, err
		}
		if _, err = fmt.Fprintf(file, "警告：本次成绩由紧急强制开闸生成\n班级：%s\n强制理由：%v\n未完成单项审核：%v\n待处理分类建议：%v\n待终裁/申诉/异议：%v\n盲审批次状态：%v\n", data.ClassName, data.GateSnapshot["forceReason"], data.GateSnapshot["unfinalizedReviews"], data.GateSnapshot["pendingClassifications"], data.GateSnapshot["pendingConflicts"], data.GateSnapshot["blindAuditStatus"]); err != nil {
			return nil, err
		}
		for _, raw := range anySlice(data.GateSnapshot["conditions"]) {
			condition := objectMap(raw)
			if _, err := fmt.Fprintf(file, "%v：%v；%v\n", condition["label"], condition["ok"], condition["detail"]); err != nil {
				return nil, err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}
