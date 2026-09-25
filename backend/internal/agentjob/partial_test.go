package agentjob

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPartialAnswerGrowsWithTheStream(t *testing.T) {
	full := `{"type":"final","thought":"直接回答","answer":"当前方案的四项权重是 60%/15%/15%/10%。","citations":[],"proposedActions":[]}`
	previous := ""
	for i := 1; i <= len(full); i++ {
		got := partialField(full[:i], "answer")
		if !strings.HasPrefix("当前方案的四项权重是 60%/15%/15%/10%。", got) {
			t.Fatalf("prefix[%d] produced text outside the answer: %q", i, got)
		}
		if len(got) < len(previous) {
			t.Fatalf("prefix[%d] shrank from %q to %q", i, previous, got)
		}
		previous = got
	}
	if previous != "当前方案的四项权重是 60%/15%/15%/10%。" {
		t.Fatalf("final answer = %q", previous)
	}
}

// 每一个切点都必须要么给出合法前缀、要么什么都不给；绝不能吐出半个转义序列。
func TestPartialAnswerNeverEmitsBrokenEscapes(t *testing.T) {
	answer := "第一行\n\"引用\"\t制表\\反斜杠   空格 😀 表情"
	encoded, err := json.Marshal(map[string]string{"answer": answer})
	if err != nil {
		t.Fatal(err)
	}
	full := `{"type":"final",` + string(encoded[1:])
	for i := 1; i <= len(full); i++ {
		got := partialField(full[:i], "answer")
		if got != "" && !strings.HasPrefix(answer, got) {
			t.Fatalf("prefix[%d] = %q is not a prefix of the answer", i, got)
		}
	}
	if got := partialField(full, "answer"); got != answer {
		t.Fatalf("complete answer = %q, want %q", got, answer)
	}
}

// thought 排在 answer 前面，所以它必须先于正文完整可读——界面上那句进度说明
// 能不能比答案早出现，全靠这个先后。
func TestPartialThoughtCompletesBeforeTheAnswerStarts(t *testing.T) {
	full := `{"type":"final","thought":"直接回答就行","answer":"当前方案的四项权重是 60%/15%/15%/10%。","citations":[]}`
	at := strings.Index(full, `"answer"`)
	if got := partialField(full[:at], "thought"); got != "直接回答就行" {
		t.Fatalf("thought before the answer field = %q", got)
	}
	if got := partialField(full[:at], "answer"); got != "" {
		t.Fatalf("answer leaked before its field arrived: %q", got)
	}
}

// thought 里出现「answer」这个词不能把提取位置带偏；字段名只在结构位置上才算数。
func TestPartialAnswerIgnoresTheWordInsideOtherFields(t *testing.T) {
	buffer := `{"type":"final","thought":"我要填 \"answer\" 字段","answer":"真正的正文`
	if got := partialField(buffer, "answer"); got != "真正的正文" {
		t.Fatalf("got %q", got)
	}
}

func TestPartialAnswerReturnsNothingBeforeTheFieldArrives(t *testing.T) {
	for _, buffer := range []string{
		``,
		`{"type":"to`,
		`{"type":"tool","thought":"先搜一下","tool":"grep","arguments":{"pattern":"权重"}}`,
		`{"type":"final","thought":"还没写到正文","answ`,
		`{"type":"final","answer"`,
		`{"type":"final","answer":`,
	} {
		if got := partialField(buffer, "answer"); got != "" {
			t.Fatalf("buffer %q produced %q", buffer, got)
		}
	}
}
