package aiassist

import (
	"slices"
	"testing"
)

// 流式缓冲区随时可能停在任何位置，只有收尾引号到了的标题才能显示——半截标题闪一下
// 再变长，比什么都不显示更难看。
func TestComposedTitlesOnlyTakesFinishedOnes(t *testing.T) {
	buffer := `{"candidates":[{"id":"c1","title":"ICPC 志愿者","claim":{}},{"id":"c2","title":"12 月体育健康`
	titles := ComposedTitles(buffer)
	if !slices.Equal(titles, []string{"ICPC 志愿者"}) {
		t.Fatalf("titles = %q", titles)
	}
}

func TestComposedTitlesDecodesEscapesAndSkipsProse(t *testing.T) {
	// note 正文里提到 "title" 不算字段：字段名前面必须是 { 或 ,。
	buffer := `{"candidates":[{"note":"表格里那列叫 \"title\"","title":"换届大会\n组织者"},{"title":""}]}`
	titles := ComposedTitles(buffer)
	if !slices.Equal(titles, []string{"换届大会\n组织者"}) {
		t.Fatalf("titles = %q", titles)
	}
}

func TestComposedTitlesStopsAtTheCap(t *testing.T) {
	buffer := ""
	for range maxPreviewTitles + 5 {
		buffer += `{"title":"一条"},`
	}
	if got := len(ComposedTitles(buffer)); got != maxPreviewTitles {
		t.Fatalf("titles = %d, want %d", got, maxPreviewTitles)
	}
}

// 预览行要把标题和它那句申报说明配在一起。学生等归组的几分钟里读到的就是这些字，
// 只给标题等于让人对着一列短语干等。
func TestComposedPreviewPairsTitleWithItsNote(t *testing.T) {
	buffer := `{"candidates":[` +
		`{"id":"candidate-1","title":"蓝桥杯省二","note":"我获得了第十六届蓝桥杯江西赛区二等奖。","confidence":0.9},` +
		`{"id":"candidate-2","title":"志愿服务","note":"我参加了雷锋月摆点志愿服务。"},` +
		`{"id":"candidate-3","title":"还没写说明的一条"`
	lines := ComposedPreview(buffer)
	want := []string{
		"蓝桥杯省二\n我获得了第十六届蓝桥杯江西赛区二等奖。",
		"志愿服务\n我参加了雷锋月摆点志愿服务。",
		"还没写说明的一条",
	}
	if len(lines) != len(want) {
		t.Fatalf("行数 = %d，期望 %d：%q", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("第 %d 行 = %q，期望 %q", i+1, lines[i], want[i])
		}
	}
}

// note 还没开始写时不能把下一条的说明错配给上一条。
func TestComposedPreviewDoesNotBorrowANoteFromAnotherCandidate(t *testing.T) {
	buffer := `{"candidates":[{"id":"c1","title":"只有标题"`
	lines := ComposedPreview(buffer)
	if len(lines) != 1 || lines[0] != "只有标题" {
		t.Fatalf("得到 %q", lines)
	}
}
