package agentjob

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// partialField 从一段还没写完的 turn JSON 里取出某个字符串字段已经生成的部分。
// 用它读 thought 和 answer：模型按 schema 先写 thought 再写 answer，所以进度说明
// 会先于正文出现在界面上。
//
// 模型是按 token 吐的，缓冲区随时可能停在任何位置：字符串中间、转义符中间、
// 甚至 \uXXXX 的一半。所以这里不能用 encoding/json 直接解（它只认完整文档），
// 而是自己定位该字段的字符串值，把已到达的片段补成一个合法的 JSON 字符串
// 再交给标准库解码——转义、Unicode 和代理对的规则一概不用自己实现。
//
// 拿不到就返回空串：调用方据此不写库，界面维持上一次的内容。
func partialField(buffer, name string) string {
	start := fieldValueStart(buffer, name)
	if start < 0 {
		return ""
	}
	body, _ := scanJSONString(buffer[start:])
	if body == "" {
		return ""
	}
	var text string
	if json.Unmarshal([]byte(`"`+body+`"`), &text) != nil {
		return ""
	}
	return text
}

// fieldValueStart 返回该字段字符串值第一个字符的下标。
//
// 只认处在 JSON 结构位置上的字段名 —— 前一个非空白字符必须是 { 或 ,。
// 否则 thought 正文里随口提到的「answer」也会被当成字段名。
func fieldValueStart(buffer, name string) int {
	key := `"` + name + `"`
	for offset := 0; ; {
		index := strings.Index(buffer[offset:], key)
		if index < 0 {
			return -1
		}
		at := offset + index
		offset = at + len(key)
		if !structuralPosition(buffer, at) {
			continue
		}
		rest := strings.TrimLeft(buffer[offset:], " \t\r\n")
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		rest = strings.TrimLeft(rest[1:], " \t\r\n")
		if !strings.HasPrefix(rest, `"`) {
			continue
		}
		return len(buffer) - len(rest) + 1
	}
}

func structuralPosition(buffer string, at int) bool {
	for i := at - 1; i >= 0; i-- {
		switch buffer[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', ',':
			return true
		default:
			return false
		}
	}
	return false
}

// scanJSONString 扫描一个 JSON 字符串字面量的内容（不含两侧引号），返回到目前
// 为止可安全解码的部分，以及它是否已经闭合。缓冲区末尾那截不完整的转义会被丢掉
// ——多等一次轮询就补齐了，而把半个 \u 交给解码器只会整段失败。
func scanJSONString(input string) (string, bool) {
	safe := 0
	for i := 0; i < len(input); {
		switch input[i] {
		case '"':
			return input[:i], true
		case '\\':
			width := 2
			if i+1 < len(input) && input[i+1] == 'u' {
				width = 6
			}
			if i+width > len(input) {
				return trimIncompleteRune(input[:safe]), false
			}
			i += width
		default:
			i++
		}
		safe = i
	}
	return trimIncompleteRune(input[:safe]), false
}

// trimIncompleteRune 砍掉尾部那个被截断的多字节字符。
//
// SSE 每个分块自身都是合法 UTF-8，正常拼接不会停在字中间，所以这是一道防御：
// 一旦哪个网关按字节转发，encoding/json 不会报错，而是悄悄把残字替换成 U+FFFD，
// 于是正文末尾每轮闪一个「�」——这种坏法很难从现象追回到这里。单测按字节遍历
// 每一个切点，把这条路径压住。
func trimIncompleteRune(value string) string {
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
