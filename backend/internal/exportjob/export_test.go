package exportjob

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"easygpa/backend/internal/scheme"
)

func BenchmarkSummaryWorkbookClassSize200(b *testing.B) {
	rows := make([]snapshotRow, 200)
	for i := range rows {
		score := float64(200 - i)
		rows[i] = snapshotRow{
			UserID: int64(i + 1), SID: fmt.Sprintf("2023%04d", i+1), Name: fmt.Sprintf("学生%d", i+1),
			CategoryScores: map[string]float64{"major": score, "practice": score / 2},
			TotalScore:     score*0.6 + score/2*0.4, ClassRank: i + 1,
		}
	}
	data := jobData{
		Config: scheme.Config{Weights: map[string]float64{"major": 0.6, "practice": 0.4}, Categories: []scheme.Category{{Key: "practice", Name: "实践创新素质"}}},
		Rows:   rows,
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := summaryWorkbook(data); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSummaryWorkbookSortsBySIDAndAddsCategoryRanks(t *testing.T) {
	data := jobData{
		Config: scheme.Config{Weights: map[string]float64{"major": 0.6, "practice": 0.4}, Categories: []scheme.Category{{Key: "practice", Name: "实践创新素质"}}},
		Rows: []snapshotRow{
			{UserID: 2, SID: "2024002", Name: "乙", CategoryScores: map[string]float64{"major": 90, "practice": 70}, TotalScore: 82, ClassRank: 1, Honor: true, AwardTier: "一等综合素质奖学金"},
			{UserID: 1, SID: "2024001", Name: "甲", CategoryScores: map[string]float64{"major": 80, "practice": 70}, TotalScore: 76, ClassRank: 2, AwardTier: "二等综合素质奖学金"},
			// 第三名没进档；迁移前的历史快照读出来也是这个空串。
			{UserID: 3, SID: "2024003", Name: "丙", CategoryScores: map[string]float64{"major": 70, "practice": 60}, TotalScore: 66, ClassRank: 3},
		},
	}
	raw, err := summaryWorkbook(data)
	if err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows("汇总")
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{"学号", "姓名", "专业素质", "专业素质排名", "实践创新素质", "实践创新素质排名", "总分", "班级排名", "三好标记", "奖学金档位"}
	if strings.Join(rows[0], "|") != strings.Join(wantHeader, "|") {
		t.Fatalf("headers = %#v, want %#v", rows[0], wantHeader)
	}
	if rows[1][0] != "2024001" || rows[2][0] != "2024002" || rows[3][0] != "2024003" {
		t.Fatalf("summary is not sorted by SID: %#v", []string{rows[1][0], rows[2][0], rows[3][0]})
	}
	if rows[1][5] != "1" || rows[2][5] != "1" || rows[3][5] != "3" {
		t.Fatalf("practice competition ranks = %q/%q/%q, want 1/1/3", rows[1][5], rows[2][5], rows[3][5])
	}
	// 未进档的那一行整行只有 9 列：excelize 不会为末尾的空单元格补位置，
	// 断言写成"第 10 列不存在或为空"才既覆盖有档位也覆盖没档位。
	if rows[1][9] != "二等综合素质奖学金" || rows[2][9] != "一等综合素质奖学金" {
		t.Fatalf("award tiers = %q/%q", rows[1][9], rows[2][9])
	}
	if len(rows[3]) > 9 && rows[3][9] != "" {
		t.Fatalf("unranked student got an award tier: %q", rows[3][9])
	}
}

func TestDeduplicateEvidencePrefersObjectETagAndFallsBackToMetadata(t *testing.T) {
	files := []evidenceFile{
		{ID: 1, SID: "001", Filename: "proof.pdf", SizeBytes: 100, ObjectETag: "ABC"},
		{ID: 2, SID: "001", Filename: "renamed.pdf", SizeBytes: 100, ObjectETag: "abc"},
		{ID: 3, SID: "002", Filename: "proof.pdf", SizeBytes: 100, ObjectETag: "abc"},
		{ID: 4, SID: "001", Filename: "legacy.zip", SizeBytes: 200},
		{ID: 5, SID: "001", Filename: "legacy.zip", SizeBytes: 200},
	}
	got := deduplicateEvidence(files)
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 3 || got[2].ID != 4 {
		t.Fatalf("deduplicated evidence = %#v", got)
	}
}

func TestDetailFormattersProduceReadableText(t *testing.T) {
	reviews := reviewsText([]any{map[string]any{"reviewer": "审核员", "reviewerSid": "001", "decision": "accepted", "score": 8.0, "reason": "**材料一致** ![](evidence:7)"}})
	if !strings.Contains(reviews, "审核员（001）｜通过｜8 分｜材料一致 [附件]") || strings.Contains(reviews, "{") || strings.Contains(reviews, "**") {
		t.Fatalf("reviews text = %q", reviews)
	}
	rule := ruleSnapshotText(map[string]any{
		"version": "v2", "categoryName": "实践创新素质",
		"item": map[string]any{"name": "基础项", "scoreRule": map[string]any{"type": "free", "min": 0.0, "max": 40.0}, "note": "至少 2 项可得 40 分"},
	})
	if !strings.Contains(rule, "自报：0—40 分") || !strings.Contains(rule, "至少 2 项") || strings.Contains(rule, "{") {
		t.Fatalf("rule text = %q", rule)
	}
}
