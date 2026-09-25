package richtext

import "testing"

func TestPlainTextDegradesMarkdown(t *testing.T) {
	input := "# 复核结论\n**材料一致**，见 ![截图](evidence:12) 与 [比赛规则](https://example.test/rule)。\n\n- `score_value` 已核对"
	want := "复核结论\n材料一致，见 [附件] 与 比赛规则。\nscore_value 已核对"
	if got := PlainText(input); got != want {
		t.Fatalf("PlainText() = %q, want %q", got, want)
	}
}

func TestPlainTextRemovesReferenceSyntax(t *testing.T) {
	input := "> 请看 [附件说明][doc]\n\n![扫描件][proof]\n[doc]: https://example.test/doc\n[proof]: evidence:8"
	want := "请看 附件说明\n[附件]"
	if got := PlainText(input); got != want {
		t.Fatalf("PlainText() = %q, want %q", got, want)
	}
}
