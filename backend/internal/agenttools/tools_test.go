package agenttools

import (
	"encoding/json"
	"strings"
	"testing"
)

// 参数报错会原样回给模型，而模型每猜错一次就是一整轮往返。所以这条错误必须同时
// 说清楚「错在哪」和「有哪些字段」——线上真出现过一条消息连猜 10 次 pattern /
// path / handle / files，直到撞满处理时限。
func TestStrictJSONNamesTheAcceptedFields(t *testing.T) {
	var input grepInput
	err := strictJSON([]byte(`{"pattern":"综合素质考评"}`), &input)
	if err == nil {
		t.Fatal("unknown field was accepted")
	}
	message := err.Error()
	for _, required := range []string{"pattern", "query", "sources", "contextLines"} {
		if !strings.Contains(message, required) {
			t.Fatalf("error %q does not mention %q", message, required)
		}
	}
}

func TestStrictJSONReportsTheWrongType(t *testing.T) {
	var input findFilesInput
	err := strictJSON([]byte(`{"limit":"三十"}`), &input)
	if err == nil || !strings.Contains(err.Error(), "limit") || !strings.Contains(err.Error(), "int") {
		t.Fatalf("error = %v", err)
	}
}

// 合法参数不能被这套检查拦下来。
func TestStrictJSONAcceptsTheDocumentedShape(t *testing.T) {
	var input grepInput
	if err := strictJSON([]byte(`{"query":"权重","sources":["src_a"],"mode":"literal","contextLines":1}`), &input); err != nil {
		t.Fatal(err)
	}
	if input.Query != "权重" || len(input.Sources) != 1 || input.ContextLines != 1 {
		t.Fatalf("input = %#v", input)
	}
}

// ArgumentFields 是提示词那份 schema 的对照来源，字段名必须和结构体标签一致，
// 顺序也要稳定（提示词测试逐个查找，乱序不影响，但空列表会让检查变成空转）。
func TestArgumentFieldsMirrorsTheStructTags(t *testing.T) {
	if got := ArgumentFields("read_text"); strings.Join(got, ",") != "source,startLine,endLine" {
		t.Fatalf("read_text fields = %v", got)
	}
	if got := ArgumentFields("current_scheme"); len(got) != 0 {
		t.Fatalf("current_scheme takes no arguments, got %v", got)
	}
	if got := ArgumentFields("shell"); got != nil {
		t.Fatalf("unknown tool returned %v", got)
	}
	for tool := range allowedTools {
		if _, ok := toolInputShapes[tool]; !ok {
			t.Fatalf("tool %q has no declared argument shape", tool)
		}
	}
}

// 文件级引用（find_files 给的整份文件）不需要读过就能交出去——用户点开拿到的是
// 原件，模型没有借它断言内容。段落级引用仍然必须真读过，否则模型可以拿一个只在
// 搜索结果里见过的条目当证据。
func TestResolvedCitationAllowsWholeFilesButNotUnreadPassages(t *testing.T) {
	executor := &Executor{handles: map[string]*Source{
		"src_file":   {kind: "document", documentID: 7},
		"src_unread": {kind: "entry", entryID: 3},
		"src_read":   {kind: "entry", entryID: 4, read: true},
	}}
	if _, ok := executor.ResolvedCitation("src_file"); !ok {
		t.Fatal("whole-file citation was dropped")
	}
	if _, ok := executor.ResolvedCitation("src_unread"); ok {
		t.Fatal("an unread passage must not be citable")
	}
	if _, ok := executor.ResolvedCitation("src_read"); !ok {
		t.Fatal("a passage that was read must stay citable")
	}
	if _, ok := executor.ResolvedCitation("src_invented"); ok {
		t.Fatal("an invented handle must not be citable")
	}
}

// 模型写检索词习惯用空格断句（「示例学院 学生综合素质考评实施细则」），
// 文件名里却没有那个空格。整串当一个子串匹配必定零命中，所以按空白拆成多个都要
// 命中的模式。
func TestLikePatternsSplitsKeywordsOnWhitespace(t *testing.T) {
	got := likePatterns("  示例学院　学生综合素质考评实施细则 PDF ")
	want := []string{"%示例学院%", "%学生综合素质考评实施细则%", "%pdf%"}
	if len(got) != len(want) {
		t.Fatalf("patterns = %q", got)
	}
	for index, pattern := range want {
		if got[index] != pattern {
			t.Fatalf("patterns[%d] = %q, want %q", index, got[index], pattern)
		}
	}
	if len(likePatterns("   ")) != 0 {
		t.Fatal("空查询必须退化成不过滤，而不是一个匹配不上任何东西的 %%%%")
	}
}

// read_text 最常见的错法是把 find_files 的文件 handle 直接递进来，错误里必须写出
// 正确路径，否则模型只会换个字段名再猜一次。
func TestReadTextRejectsFileHandlesWithTheFixInTheMessage(t *testing.T) {
	executor := &Executor{handles: map[string]*Source{"src_file": {kind: "document", documentID: 7}}}
	_, err := executor.readText(t.Context(), json.RawMessage(`{"source":"src_file"}`))
	if err == nil || !strings.Contains(err.Error(), "grep") {
		t.Fatalf("error = %v", err)
	}
}

func TestScanBudgetIsCumulativeAndStaysExhausted(t *testing.T) {
	executor := &Executor{maxScanBytes: 10}
	if err := executor.chargeScan(6); err != nil {
		t.Fatal(err)
	}
	if err := executor.chargeScan(5); err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("budget overflow error = %v", err)
	}
	if err := executor.chargeScan(1); err == nil {
		t.Fatal("exhausted scan budget was reset after an error")
	}
}

func TestProductScorePrefersCustomNavigationAndCurrentPage(t *testing.T) {
	terms := productTerms("在哪里交材料")
	builtin, _ := productScore("在哪里交材料", terms, "学生提交材料指南", []string{"stuHome"}, []string{"在哪里交材料"}, "进入提交材料页面。", "stuSubmit", "builtin")
	custom, _ := productScore("在哪里交材料", terms, "本校入口说明", []string{"stuSubmit"}, []string{"交材料"}, "从教务入口进入提交材料。", "stuSubmit", "custom")
	if custom <= builtin {
		t.Fatalf("custom score %d did not outrank builtin score %d", custom, builtin)
	}
}

func TestProductExcerptCentersChineseMatch(t *testing.T) {
	text := strings.Repeat("前", 180) + "提交材料" + strings.Repeat("后", 400)
	index := strings.Index(text, "提交材料")
	excerpt := productExcerpt(text, productTerms("提交材料"), index)
	if !strings.Contains(excerpt, "提交材料") || len([]rune(excerpt)) > 850 {
		t.Fatalf("excerpt did not preserve the match: %q", excerpt)
	}
}
