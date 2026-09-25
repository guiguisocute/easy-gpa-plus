// Package richtext contains degradation helpers for markdown content that is
// rendered in channels without a markdown parser (mail and tabular exports).
package richtext

import (
	"regexp"
	"strings"
)

var (
	markdownImage       = regexp.MustCompile(`!\[[^\]]*\]\([^\n)]*\)`)
	markdownImageRef    = regexp.MustCompile(`!\[[^\]]*\]\[[^\]]*\]`)
	markdownLink        = regexp.MustCompile(`\[([^\]]+)\]\([^\n)]*\)`)
	markdownLinkRef     = regexp.MustCompile(`\[([^\]]+)\]\[[^\]]*\]`)
	markdownLinkDef     = regexp.MustCompile(`^\s*\[[^\]]+\]:\s*\S+.*$`)
	markdownHeading     = regexp.MustCompile(`^\s{0,3}#{1,6}\s*`)
	markdownQuote       = regexp.MustCompile(`^\s{0,3}>\s?`)
	markdownList        = regexp.MustCompile(`^\s*(?:[-+*]|\d+[.)])\s+`)
	markdownRule        = regexp.MustCompile(`^\s*(?:[-*_]\s*){3,}$`)
	markdownEscapedMark = regexp.MustCompile(`\\([\\` + "`" + `*_[\]{}()#+.!>~-])`)
)

// PlainText preserves the words in markdown while removing presentation-only
// syntax. Embedded images intentionally become a stable placeholder because
// mail and spreadsheet cells cannot reproduce authenticated evidence URLs.
func PlainText(markdown string) string {
	value := strings.ReplaceAll(markdown, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = markdownImage.ReplaceAllString(value, "[附件]")
	value = markdownImageRef.ReplaceAllString(value, "[附件]")
	value = markdownLink.ReplaceAllString(value, "$1")
	value = markdownLinkRef.ReplaceAllString(value, "$1")

	lines := strings.Split(value, "\n")
	plain := make([]string, 0, len(lines))
	for _, line := range lines {
		if markdownLinkDef.MatchString(line) || markdownRule.MatchString(line) {
			continue
		}
		line = markdownHeading.ReplaceAllString(line, "")
		line = markdownQuote.ReplaceAllString(line, "")
		line = markdownList.ReplaceAllString(line, "")
		line = strings.ReplaceAll(line, "`", "")
		line = strings.NewReplacer("**", "", "__", "", "~~", "", "*", "").Replace(line)
		line = stripEmphasisUnderscores(line)
		line = markdownEscapedMark.ReplaceAllString(line, "$1")
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			plain = append(plain, line)
		}
	}
	return strings.Join(plain, "\n")
}

func stripEmphasisUnderscores(value string) string {
	runes := []rune(value)
	result := make([]rune, 0, len(runes))
	for index, current := range runes {
		if current != '_' {
			result = append(result, current)
			continue
		}
		var before, after rune
		if index > 0 {
			before = runes[index-1]
		}
		if index+1 < len(runes) {
			after = runes[index+1]
		}
		// Keep underscores inside identifiers while removing markdown emphasis.
		if isWordRune(before) && isWordRune(after) {
			result = append(result, current)
		}
	}
	return string(result)
}

func isWordRune(value rune) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
