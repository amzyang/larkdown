package core

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/util"
)

// escapeContext 描述一段 TextRun 文本所处的 markdown 上下文，决定转义规则。
type escapeContext struct {
	atLineStart  bool // 串首处于行首：块级触发字符（# - + > | = 有序标记）需防护
	inTableCell  bool // GFM cell 上下文：| 全位置转义；cell 内容永不处于行首
	skipBareURLs bool // 跳过裸 URL span：linkify 的 URL 正则不含 \，转义会把链接截成两半
}

// escapeMarkdownText 对飞书正文文本做 CommonMark backslash 转义，
// 防止字面字符被上传侧（goldmark）解析成 markdown 结构。
// 与上传侧 unescapeMarkdownText 成对：块签名经「转义→解析→反转义」后与远端收敛。
//
// 行内集（任意位置）：\ * ` [ ] ~ < $；_ 仅在非 intraword 位置转义
// （CommonMark 的 _ 强调不支持 intraword，snake_case 零噪音）。
// 行首集（含内容中 \n 之后的行首）：见 lineStartTriggerIndex。
// & ! ( ) 不转义：无行内活性（entity 在本管线不被 goldmark 解码，& 转义反而不收敛）。
func escapeMarkdownText(s string, ctx escapeContext) string {
	if s == "" {
		return s
	}
	var out strings.Builder
	out.Grow(len(s) + 8)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		atStart := (ctx.atLineStart || i > 0) && !ctx.inTableCell
		escapeMarkdownLine(&out, line, atStart, ctx)
	}
	return out.String()
}

func escapeMarkdownLine(out *strings.Builder, line string, atLineStart bool, ctx escapeContext) {
	forceIdx := -1
	if atLineStart {
		forceIdx = lineStartTriggerIndex(line)
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if ctx.skipBareURLs && isBareURLStart(line, i) {
			end := bareURLEnd(line, i)
			out.WriteString(line[i:end])
			i = end - 1
			continue
		}
		if i == forceIdx {
			out.WriteByte('\\')
			out.WriteByte(c)
			continue
		}
		switch c {
		case '\\', '*', '`', '[', ']', '~', '<', '$':
			out.WriteByte('\\')
			out.WriteByte(c)
		case '_':
			if underscoreIntraword(line, i) {
				out.WriteByte(c)
			} else {
				out.WriteString(`\_`)
			}
		case '|':
			if ctx.inTableCell {
				out.WriteString(`\|`)
			} else {
				out.WriteByte(c)
			}
		default:
			out.WriteByte(c)
		}
	}
}

// lineStartTriggerIndex 返回行首块级触发字符需转义的字节下标，无则 -1。
// 覆盖：ATX 标题、bullet 列表、thematic break、setext 下划线、blockquote、
// GFM 表格行、有序列表标记（转义 . / )）。* ` ~ < $ [ 已被行内集覆盖，无需在此处理。
func lineStartTriggerIndex(line string) int {
	// 跳过至多 3 个前导空格（≥4 空格属 indented code，见 CLAUDE.md 豁免清单）
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) {
		return -1
	}
	rest := line[i:]
	switch c := rest[0]; c {
	case '#':
		j := 0
		for j < len(rest) && rest[j] == '#' {
			j++
		}
		if j <= 6 && (j == len(rest) || rest[j] == ' ' || rest[j] == '\t') {
			return i
		}
	case '-':
		if len(rest) == 1 || rest[1] == ' ' || rest[1] == '\t' {
			return i
		}
		if strings.Trim(rest, "- \t") == "" { // ---（thematic break / setext H2）
			return i
		}
	case '+':
		if len(rest) == 1 || rest[1] == ' ' || rest[1] == '\t' {
			return i
		}
	case '>':
		return i // blockquote 标记后无需空格
	case '|':
		return i // 行首 | 可能与后续行组成 GFM 表格
	case '=':
		if strings.Trim(rest, "= \t") == "" { // setext H1 下划线
			return i
		}
	default:
		if c >= '0' && c <= '9' {
			j := 0
			for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
				j++
			}
			// CommonMark 有序列表标记最多 9 位数字，且 ./) 后须跟空白或行尾
			if j <= 9 && j < len(rest) && (rest[j] == '.' || rest[j] == ')') {
				if j+1 == len(rest) || rest[j+1] == ' ' || rest[j+1] == '\t' {
					return i + j
				}
			}
		}
	}
	return -1
}

// isBareURLStart 判断 line[i:] 是否在词边界上以裸 URL 起始（GFM linkify 可识别的形态）。
func isBareURLStart(line string, i int) bool {
	if i > 0 {
		switch line[i-1] {
		case ' ', '\t', '(':
		default:
			return false
		}
	}
	rest := line[i:]
	for _, prefix := range []string{"http://", "https://", "www."} {
		if len(rest) > len(prefix) && strings.EqualFold(rest[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// bareURLEnd 返回裸 URL span 的结束下标（GFM autolink 终止于空白与 <）。
func bareURLEnd(line string, i int) int {
	j := i
	for j < len(line) && line[j] != ' ' && line[j] != '\t' && line[j] != '<' {
		j++
	}
	return j
}

// underscoreIntraword 判断 line[i] 处的 _ 是否两侧均为字母/数字（intraword，
// CommonMark 不会解析为强调，无需转义）。串边界视为非字母数字（宁可过转义）。
func underscoreIntraword(line string, i int) bool {
	prev, size := utf8.DecodeLastRuneInString(line[:i])
	if size == 0 || !isWordRune(prev) {
		return false
	}
	next, size := utf8.DecodeRuneInString(line[i+1:])
	return size > 0 && isWordRune(next)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// unescapeMarkdownText 按 CommonMark 规则剥 backslash 转义（\+ASCII 标点 → 标点），
// 是 escapeMarkdownText 的逆变换；\+非标点按原文保留。
// goldmark 解析期不剥 \（转义字符原样留在 Text segment），故上传侧必须手动反转义，
// 否则下载产物/手写 markdown 里的 \_ 会被字面上传、块签名与远端永不收敛。
func unescapeMarkdownText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); i++ {
		// util.IsPunct 即 CommonMark 可转义 ASCII 标点集（goldmark 同源判定）
		if s[i] == '\\' && i+1 < len(s) && util.IsPunct(s[i+1]) {
			out.WriteByte(s[i+1])
			i++
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}
