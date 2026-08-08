package core

import (
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

// cell 内 file 块文件名含 | 不转义会切列（GFM 按原始文本的 | 切分 cell）。
func TestParseDocxBlockFileInCellEscapesPipe(t *testing.T) {
	p := NewParser(NewConfig("", "").Output, nil)
	p.inTableCell = true
	got := p.ParseDocxBlockFile(&lark.DocxBlockFile{
		Name:  "a|b.pdf",
		Token: "FILETOKEN",
	})
	assert.Equal(t, "[a\\|b.pdf](FILETOKEN)\n", got)
}

// cell 内链接 destination 含 | 会切列，且 destination 位不能 backslash 转义，
// 须与 sheet 链接同法 percent-encode 为 %7C（签名双侧过 UnescapeURL 归一不漂移）；
// 非 cell 上下文保持原样不编码。
func TestParseDocxTextRunLinkDestPipe(t *testing.T) {
	tr := &lark.DocxTextElementTextRun{
		Content: "x",
		TextElementStyle: &lark.DocxTextElementStyle{
			Link: &lark.DocxTextElementStyleLink{URL: "https://a.com/b|c?q=1|2"},
		},
	}

	p := NewParser(NewConfig("", "").Output, nil)
	assert.Equal(t, "[x](https://a.com/b|c?q=1|2)", p.ParseDocxTextElementTextRun(tr))

	p.inTableCell = true
	assert.Equal(t, "[x](https://a.com/b%7Cc?q=1%7C2)", p.ParseDocxTextElementTextRun(tr))
}
