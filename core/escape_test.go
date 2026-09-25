package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeMarkdownText(t *testing.T) {
	plain := escapeContext{atLineStart: true, skipBareURLs: true}
	inline := escapeContext{atLineStart: false, skipBareURLs: true}
	label := escapeContext{atLineStart: false, skipBareURLs: false} // 链接文本：linkify 在 label 内短路，全量转义
	cell := escapeContext{inTableCell: true, skipBareURLs: true}

	tests := []struct {
		name string
		in   string
		ctx  escapeContext
		want string
	}{
		// —— 行内活性字符（任意位置） ——
		{"star", "a *b* c", inline, `a \*b\* c`},
		{"backtick", "run `ls` now", inline, "run \\`ls\\` now"},
		{"brackets", "[tag] x[0]", inline, `\[tag\] x\[0\]`},
		{"tilde", "~/.config 和 ~/.cache", inline, `\~/.config 和 \~/.cache`},
		{"angle", "a<b 且 <br>", inline, `a\<b 且 \<br>`},
		{"dollar", "价格 $100 和 $200", inline, `价格 \$100 和 \$200`},
		{"backslash", `a\b\\c`, inline, `a\\b\\\\c`},
		{"backslash first", `\_x`, inline, `\\\_x`},

		// —— `_` intraword 豁免 ——
		{"snake_case intraword", "snake_case_name", inline, "snake_case_name"},
		{"underscore emphasis", "he said _yes_ ok", inline, `he said \_yes\_ ok`},
		{"underscore lead", "_lead", inline, `\_lead`},
		{"underscore tail", "tail_", inline, `tail\_`},
		{"underscore cjk intraword", "中_文", inline, "中_文"},
		{"underscore after punct", "a/_b", inline, `a/\_b`},

		// —— 裸 URL 跳过（skipBareURLs=true 且非 label） ——
		{"bare url skip", "see https://a.com/_x?q=1*2 end", inline, "see https://a.com/_x?q=1*2 end"},
		{"bare www skip", "www.a.com/_x here", plain, "www.a.com/_x here"},
		{"url in label escaped", "https://example.com/_abc", label, `https://example.com/\_abc`},
		{"url mid word not skipped", "foohttps://a.com/_x", inline, `foohttps://a.com/\_x`},

		// —— 行首块级触发字符（仅 atLineStart） ——
		{"heading", "# 标题", plain, `\# 标题`},
		{"heading h2", "## 标题", plain, `\## 标题`},
		{"heading no space", "#tag", plain, "#tag"},
		{"heading only", "#", plain, `\#`},
		{"dash list", "- item", plain, `\- item`},
		{"dash no space", "-x", plain, "-x"},
		{"dash word", "--force", plain, "--force"},
		{"thematic break", "---", plain, `\---`},
		{"thematic break spaced", "- - -", plain, `\- - -`},
		{"plus list", "+ item", plain, `\+ item`},
		{"quote marker", "> quoted", plain, `\> quoted`},
		{"quote marker no space", ">quoted", plain, `\>quoted`},
		{"ordered dot", "1. item", plain, `1\. item`},
		{"ordered paren", "10) item", plain, `10\) item`},
		{"decimal not list", "1.5 万", plain, "1.5 万"},
		{"setext equals", "===", plain, `\===`},
		{"equals text", "=x", plain, "=x"},
		{"pipe line start", "| a |", plain, `\| a |`},
		{"leading spaces then trigger", "  - item", plain, `  \- item`},
		{"not line start", "# 标题", inline, "# 标题"},
		{"after newline", "x\n- y", inline, "x\n\\- y"},
		{"after newline heading", "x\n# y", inline, "x\n\\# y"},

		// —— cell 上下文：| 全位置，无行首语义 ——
		{"cell pipe", "a | b", cell, `a \| b`},
		{"cell no line start", "- item", cell, "- item"},
		{"cell inline set", "a *b*", cell, `a \*b\*`},

		// —— 中性字符不转义 ——
		{"neutral", "a & b (c) d! e.f", inline, "a & b (c) d! e.f"},

		// —— 字符引用：只转义会被 decoder 解码的 & ——
		{"entity named", "a &amp; b", inline, `a \&amp; b`},
		{"entity numeric", "竖线 &#124;", inline, `竖线 \&#124;`},
		{"entity hex", "&#x26; 号", inline, `\&#x26; 号`},
		{"entity unknown name", "&notanentity; x", inline, "&notanentity; x"},
		{"entity no semicolon", "?a=1&amp=2", inline, "?a=1&amp=2"},
		{"query separator", "?a=1&b=2", inline, "?a=1&b=2"},
		{"entity numeric overlong", "&#12345678; x", inline, "&#12345678; x"},
		{"entity hex overlong", "&#x1234567; x", inline, "&#x1234567; x"},
		{"entity numeric max", "&#1234567; x", inline, `\&#1234567; x`},
		{"entity hex max", "&#x123456; x", inline, `\&#x123456; x`},
		// 元素末尾未收尾的引用前缀：拼接相邻同款式 TextRun 后才凑成完整引用
		{"charref prefix at end", "a&am", inline, `a\&am`},
		{"charref numeric prefix at end", "a&#12", inline, `a\&#12`},
		{"lone ampersand at end", "a&", inline, "a&"},
		{"mid pipe outside cell", "a | b", inline, "a | b"},

		{"empty", "", plain, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, escapeMarkdownText(tt.in, tt.ctx))
		})
	}
}

func TestUnescapeMarkdownText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"underscore", `a \_b\_ c`, "a _b_ c"},
		{"double backslash", `a\\b`, `a\b`},
		{"all punct", "\\*\\`\\[\\]\\~\\<\\$\\#\\-\\+\\>\\|\\=\\.\\)", "*`[]~<$#-+>|=.)"},
		{"non punct kept", `a\b \中`, `a\b \中`},
		{"trailing backslash", `a\`, `a\`},
		{"no backslash fast path", "plain text", "plain text"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, unescapeMarkdownText(tt.in))
		})
	}
}

// 电子表格/多维表格 cell 值进 GFM 表格行：| 不转义会切列、换行会断行。
func TestSheetCellText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a | b", `a \| b`},
		{"多行\n值", "多行<br>值"},
		{"=IF(A1>0, \"x|y\", B1)\n", `=IF(A1>0, "x\|y", B1)<br>`},
		{"plain", "plain"},
	}
	for _, tt := range tests {
		if got := sheetCellText(tt.in); got != tt.want {
			t.Errorf("sheetCellText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// escapeMarkdownText 与上传侧 goldmark decoder 互逆：下载产物经解析后须还原为原文。
// 二者任一侧漏一类字符，块签名就与远端永不收敛。
func TestEscapeDecodeRoundTrip(t *testing.T) {
	cases := []string{
		`a \* b _c_ [d] ~e~ <f> $g$`,
		"a &amp; b",
		"&#124; 与 &#x26; 与 &sect;",
		"&notanentity; 与 ?a=1&b=2",
		"&#12345678; 与 &#x1234567;",
		// 裸 URL 的尾巴被 linkify 裁掉后按普通文本解析，必须照常转义
		"见 https://x.com/?a=1&amp; 结束",
		"见 https://x.com/a&amp;. 结束",
		"见 https://x.com/path~ 结束",
		"见 https://x.com/a(b)c 与 https://x.com/a_b 结束",
		"snake_case 与 中_文",
		`路径 C:\\tmp\\x`,
		"[!NOTE] 不是 callout",
		"# 不是标题",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			// skipBareURLs 与下载侧无 Link 样式的 TextRun 一致（parser.go）
			escaped := escapeMarkdownText(in, escapeContext{atLineStart: true, skipBareURLs: true})
			result, err := ConvertMarkdownToDocxBlocks(escaped, "")
			assert.NoError(t, err)
			var got string
			for _, b := range result.TopBlocks {
				if b == nil || b.Text == nil {
					continue
				}
				for _, e := range b.Text.Elements {
					if e.TextRun != nil {
						got += e.TextRun.Content
					}
				}
			}
			assert.Equal(t, in, got)
		})
	}
}
