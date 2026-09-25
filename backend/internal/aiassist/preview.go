package aiassist

import (
	"encoding/json"
	"strings"
)

// previewTitles 上限。归组一次最多也就几十条候选，多了对"看见东西"这件事没有增益，
// 反倒会把面板刷满。
const maxPreviewTitles = 40

// ComposedTitles 从一段还没写完的归组 JSON 里，摘出已经写完的候选标题。
//
// 归组是一次性调用：材料一多要跑好几分钟，而进度条早在逐张识别结束时就满了，界面
// 上什么都不动。把流式输出里陆续成型的标题显示出来，等待期间至少有东西可看。
//
// 模型是按 token 吐的，缓冲区随时可能停在任意位置——字符串中间、转义符中间、甚至
// \uXXXX 的一半。所以不能用 encoding/json 直接解（它只认完整文档），而是自己定位
// 每个 title 字段的字符串值；只有收尾引号已经到达的才算数，转义与 Unicode 仍旧交
// 给标准库解码。
func ComposedTitles(buffer string) []string {
	return composedStrings(buffer, "title")
}

// ComposedPreview 把每条候选的标题和它下面那句申报说明配成一行，中间用换行分隔。
//
// 只报标题时，等待期间能看的只是一列短语，看不出模型在干什么，几分钟下来就是干等。
// 把 note 一并带出来，学生读到的是"我参加了…"这种即将署他名提交的正文——既是进度，
// 也能提前发现写歪了的地方。
//
// schema 里 note 排在 title 后面，所以第 N 个 title 配第 N 个 note；note 还没写到时
// 这一行就只有标题。
func ComposedPreview(buffer string) []string {
	titles := composedStrings(buffer, "title")
	notes := composedStrings(buffer, "note")
	lines := make([]string, 0, len(titles))
	for index, title := range titles {
		if index < len(notes) && notes[index] != "" {
			lines = append(lines, title+"\n"+notes[index])
			continue
		}
		lines = append(lines, title)
	}
	return lines
}

func composedStrings(buffer, field string) []string {
	titles := make([]string, 0, 8)
	for offset := 0; len(titles) < maxPreviewTitles; {
		index := strings.Index(buffer[offset:], `"`+field+`"`)
		if index < 0 {
			return titles
		}
		at := offset + index
		offset = at + len(field) + 2
		if !structuralKey(buffer, at) {
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
		body, closed := scanJSONString(rest[1:])
		if !closed {
			return titles
		}
		offset = len(buffer) - len(rest) + 1 + len(body) + 1
		var title string
		if json.Unmarshal([]byte(`"`+body+`"`), &title) != nil {
			continue
		}
		if title = strings.TrimSpace(title); title != "" {
			titles = append(titles, title)
		}
	}
	return titles
}

// structuralKey 要求字段名前面那个非空白字符是 { 或 ,，否则别处正文里出现的
// "title" 也会被当成字段名。
func structuralKey(buffer string, at int) bool {
	for index := at - 1; index >= 0; index-- {
		switch buffer[index] {
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

// scanJSONString 从字符串内容的第一个字符开始扫，返回原样的内容和它是否已经收尾。
// 反斜杠后面的一个字符一律跳过，这样 \" 不会被误判成结束引号。
func scanJSONString(rest string) (string, bool) {
	for index := 0; index < len(rest); index++ {
		switch rest[index] {
		case '\\':
			index++
		case '"':
			return rest[:index], true
		}
	}
	return rest, false
}
