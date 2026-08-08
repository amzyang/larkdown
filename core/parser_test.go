package core_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDocxContent(t *testing.T) {
	root := utils.RootDir()

	testdata := []string{
		"testdocx.1",
		"testdocx.2",
		"testdocx.3",
		"testdocx.folded",
		"testdocx.mention_user",
		"testdocx.mention_doc",
		"testdocx.nested_todos",
		"testdocx.table_cell_code",
		"testdocx.escape",
	}
	for _, td := range testdata {
		t.Run(td, func(t *testing.T) {
			jsonFile, err := os.Open(path.Join(root, "testdata", td+".json"))
			require.NoError(t, err)
			defer jsonFile.Close()

			data := struct {
				Document *lark.DocxDocument `json:"document"`
				Blocks   []*lark.DocxBlock  `json:"blocks"`
			}{}
			byteValue, _ := io.ReadAll(jsonFile)
			json.Unmarshal(byteValue, &data)

			parser := core.NewParser(core.NewConfig("", "").Output, nil)
			mdParsed := parser.ParseDocxContent(data.Document, data.Blocks)
			fmt.Println(mdParsed)

			mdFile, err := os.ReadFile(path.Join(root, "testdata", td+".md"))
			require.NoError(t, err)
			mdExpected := string(mdFile)

			assert.Equal(t, mdExpected, mdParsed)
		})
	}
}

// 代码块内容含 ``` 行时固定三反引号围栏会被提前闭合（内容逃逸为正文），
// 围栏须按内容最长反引号 run 动态加长。
func TestParseDocxBlockCodeFenceLengthens(t *testing.T) {
	block := &lark.DocxBlock{
		BlockID:   "c1",
		BlockType: lark.DocxBlockTypeCode,
		Code: &lark.DocxBlockText{
			Style: &lark.DocxTextStyle{},
			Elements: []*lark.DocxTextElement{
				{TextRun: &lark.DocxTextElementTextRun{
					Content:          "outer:\n```js\ncode\n```",
					TextElementStyle: &lark.DocxTextElementStyle{},
				}},
			},
		},
	}
	parser := core.NewParser(core.NewConfig("", "").Output, nil)
	got := parser.ParseDocxBlock(block, 0)
	assert.Equal(t, "````\nouter:\n```js\ncode\n```\n````\n", got)
}
