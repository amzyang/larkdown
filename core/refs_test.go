package core_test

import (
	"encoding/json"
	"os"
	"path"
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocRefFromMention(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		objType string
		url     string
		wantOK  bool
		wantURL string
	}{
		{"docx", "TokDocx001", "docx", "https://x.feishu.cn/docx/TokDocx001", true, "https://x.feishu.cn/docx/TokDocx001"},
		{"wiki", "TokWiki005", "wiki", "https://x.feishu.cn/wiki/TokWiki005", true, "https://x.feishu.cn/wiki/TokWiki005"},
		{"归一化剥离 query", "TokDocx001", "docx", "https://x.feishu.cn/docx/TokDocx001?from=space", true, "https://x.feishu.cn/docx/TokDocx001"},
		{"旧版 doc 排除", "TokDoc002", "doc", "https://x.feishu.cn/docs/TokDoc002", false, ""},
		{"sheet 排除", "TokSheet003", "sheet", "https://x.feishu.cn/sheets/TokSheet003", false, ""},
		{"bitable 排除", "TokBitable004", "bitable", "https://x.feishu.cn/base/TokBitable004", false, ""},
		{"mindnote 排除", "TokMindnote006", "mindnote", "https://x.feishu.cn/mindnotes/TokMindnote006", false, ""},
		{"file 排除", "TokFile007", "file", "https://x.feishu.cn/file/TokFile007", false, ""},
		{"slide 排除", "TokSlide008", "slide", "https://x.feishu.cn/slides/TokSlide008", false, ""},
		{"空 token 排除", "", "docx", "https://x.feishu.cn/docx/Tok", false, ""},
		{"无效 URL 排除", "Tok", "docx", "not-a-url", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, ok := core.DocRefFromMention(tt.token, tt.objType, tt.url, "标题")
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.token, ref.Token)
				assert.Equal(t, tt.objType, ref.ObjType)
				assert.Equal(t, tt.wantURL, ref.URL)
			}
		})
	}
}

func TestDocRefFromLink(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOK    bool
		wantToken string
		wantType  string
		wantURL   string
	}{
		{"docx", "https://x.feishu.cn/docx/TokA", true, "TokA", "docx", "https://x.feishu.cn/docx/TokA"},
		{"docx 带 query", "https://x.feishu.cn/docx/TokA?from=space", true, "TokA", "docx", "https://x.feishu.cn/docx/TokA"},
		{"wiki node", "https://x.feishu.cn/wiki/TokB", true, "TokB", "wiki", "https://x.feishu.cn/wiki/TokB"},
		{"wiki 带锚点", "https://x.feishu.cn/wiki/TokB#heading-1", true, "TokB", "wiki", "https://x.feishu.cn/wiki/TokB"},
		{"larksuite 域名", "https://xx.larksuite.com/docx/TokC", true, "TokC", "docx", "https://xx.larksuite.com/docx/TokC"},
		{"wiki settings 排除", "https://x.feishu.cn/wiki/settings/7123", false, "", "", ""},
		{"wiki space 排除", "https://x.feishu.cn/wiki/space/7123", false, "", "", ""},
		{"folder 排除", "https://x.feishu.cn/drive/folder/TokD", false, "", "", ""},
		{"file 排除", "https://x.feishu.cn/file/TokE", false, "", "", ""},
		{"sheets 排除", "https://x.feishu.cn/sheets/TokF", false, "", "", ""},
		{"普通外链排除", "https://example.com/page", false, "", "", ""},
		{"非 https 排除", "http://x.feishu.cn/docx/TokG", false, "", "", ""},
		{"相对链接排除", "static/img.png", false, "", "", ""},
		{"锚点链接排除", "#6-sse-协议规范", false, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, ok := core.DocRefFromLink(tt.url, "文本")
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.wantToken, ref.Token)
				assert.Equal(t, tt.wantType, ref.ObjType)
				assert.Equal(t, tt.wantURL, ref.URL)
				assert.Equal(t, "文本", ref.Title)
			}
		})
	}
}

func TestRefCollector(t *testing.T) {
	t.Run("去重与 Drain", func(t *testing.T) {
		c := core.NewRefCollector()
		a := core.DocRef{Token: "TokA", ObjType: "docx", URL: "https://x.feishu.cn/docx/TokA"}
		b := core.DocRef{Token: "TokB", ObjType: "wiki", URL: "https://x.feishu.cn/wiki/TokB"}
		c.Add(a, b, a)
		c.Add(b)
		assert.Equal(t, []core.DocRef{a, b}, c.Drain())
		// Drain 清空待取列表，但去重集保留：同一 token 不会二次产出
		c.Add(a)
		assert.Empty(t, c.Drain())
	})

	t.Run("空 token 忽略", func(t *testing.T) {
		c := core.NewRefCollector()
		c.Add(core.DocRef{Token: ""})
		assert.Empty(t, c.Drain())
	})

	t.Run("nil 接收者 no-op", func(t *testing.T) {
		var c *core.RefCollector
		c.Add(core.DocRef{Token: "TokA"})
		assert.Nil(t, c.Drain())
	})
}

// parseRefDocs 用 parser 解析 fixture 风格的 JSON，返回旁路收集到的 RefDocs。
func parseRefDocs(t *testing.T, raw []byte) []core.DocRef {
	t.Helper()
	data := struct {
		Document *lark.DocxDocument `json:"document"`
		Blocks   []*lark.DocxBlock  `json:"blocks"`
	}{}
	require.NoError(t, json.Unmarshal(raw, &data))
	parser := core.NewParser(core.NewConfig("", "").Output, nil)
	parser.ParseDocxContent(data.Document, data.Blocks)
	return parser.RefDocs
}

// 含行内链接的最小文档：飞书 docx 链接、wiki 链接（带锚点）、外链、同 token 重复引用。
const linkDocJSON = `{
  "document": {"document_id": "doxcnLinkTest", "revision_id": 1, "title": "Link 测试"},
  "blocks": [
    {
      "block_id": "doxcnLinkTest",
      "children": ["blk_t1", "blk_t2"],
      "block_type": 1,
      "page": {"elements": [{"text_run": {"content": "Link 测试"}}]}
    },
    {
      "block_id": "blk_t1",
      "parent_id": "doxcnLinkTest",
      "block_type": 2,
      "text": {"elements": [
        {"text_run": {"content": "规范", "text_element_style": {"link": {"url": "https%3A%2F%2Fx.feishu.cn%2Fdocx%2FTokLinkA"}}}},
        {"text_run": {"content": " 与 "}},
        {"text_run": {"content": "节点", "text_element_style": {"link": {"url": "https%3A%2F%2Fx.feishu.cn%2Fwiki%2FTokLinkB%23h1"}}}},
        {"text_run": {"content": " 及 "}},
        {"text_run": {"content": "外链", "text_element_style": {"link": {"url": "https%3A%2F%2Fexample.com%2Fpage"}}}}
      ]}
    },
    {
      "block_id": "blk_t2",
      "parent_id": "doxcnLinkTest",
      "block_type": 2,
      "text": {"elements": [
        {"text_run": {"content": "再引规范", "text_element_style": {"link": {"url": "https%3A%2F%2Fx.feishu.cn%2Fdocx%2FTokLinkA"}}}}
      ]}
    }
  ]
}`

func TestParserCollectsRefDocs(t *testing.T) {
	t.Run("mention 只收 docx 与 wiki", func(t *testing.T) {
		raw, err := os.ReadFile(path.Join(utils.RootDir(), "testdata", "testdocx.mention_doc.json"))
		require.NoError(t, err)
		refs := parseRefDocs(t, raw)
		assert.Equal(t, []core.DocRef{
			{Token: "TokDocx001", ObjType: "docx", URL: "https://x.feishu.cn/docx/TokDocx001", Title: "新版文档"},
			{Token: "TokWiki005", ObjType: "wiki", URL: "https://x.feishu.cn/wiki/TokWiki005", Title: "知识库节点"},
		}, refs)
	})

	t.Run("行内链接过滤外链并按 token 去重", func(t *testing.T) {
		refs := parseRefDocs(t, []byte(linkDocJSON))
		assert.Equal(t, []core.DocRef{
			{Token: "TokLinkA", ObjType: "docx", URL: "https://x.feishu.cn/docx/TokLinkA", Title: "规范"},
			{Token: "TokLinkB", ObjType: "wiki", URL: "https://x.feishu.cn/wiki/TokLinkB", Title: "节点"},
		}, refs)
	})
}
