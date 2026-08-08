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
