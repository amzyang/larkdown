package core

import (
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMentionObjTypeMapping(t *testing.T) {
	cases := []struct {
		str string
		typ lark.DocxMentionObjType
	}{
		{"doc", lark.DocxMentionObjTypeDoc},
		{"sheet", lark.DocxMentionObjTypeSheet},
		{"bitable", lark.DocxMentionObjTypeBitable},
		{"mindnote", lark.DocxMentionObjTypeMindNote},
		{"file", lark.DocxMentionObjTypeFile},
		{"slide", lark.DocxMentionObjTypeSlide},
		{"wiki", lark.DocxMentionObjTypeWiki},
		{"docx", lark.DocxMentionObjTypeDocx},
	}
	for _, c := range cases {
		assert.Equal(t, c.str, mentionObjTypeString(c.typ), "int→str %d", c.typ)
		assert.Equal(t, c.typ, mentionObjTypeFromString(c.str), "str→int %s", c.str)
	}
	// 未知值兜底为新版文档 docx
	assert.Equal(t, "docx", mentionObjTypeString(lark.DocxMentionObjType(999)))
	assert.Equal(t, lark.DocxMentionObjTypeDocx, mentionObjTypeFromString("unknown"))
	assert.Equal(t, lark.DocxMentionObjTypeDocx, mentionObjTypeFromString(""))
}

func TestXMLEscape(t *testing.T) {
	assert.Equal(t, "a &amp; b &lt;x&gt;", xmlEscapeText("a & b <x>"))
	assert.Equal(t, `say &quot;hi&quot; &amp; &lt;b&gt;`, xmlEscapeAttr(`say "hi" & <b>`))
	// 不重复转义
	assert.Equal(t, "&amp;amp;", xmlEscapeText("&amp;"))
}

func TestConvertCiteMentionUser(t *testing.T) {
	md := `请联系 <cite type="user" user-id="on_union_abc">张三</cite> 审阅`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elems := result.TopBlocks[0].Text.Elements
	require.Len(t, elems, 3)
	assert.Equal(t, "请联系 ", elems[0].TextRun.Content)
	require.NotNil(t, elems[1].MentionUser)
	assert.Equal(t, "on_union_abc", elems[1].MentionUser.UserID)
	assert.Nil(t, elems[1].TextRun)
	assert.Equal(t, " 审阅", elems[2].TextRun.Content)

	// 显示名记录到 MentionUserNames，供写入失败时降级使用
	assert.Equal(t, "张三", result.MentionUserNames["on_union_abc"])
}

func TestConvertCiteMentionDoc(t *testing.T) {
	md := `详见 <cite type="doc" doc-id="TOK1" obj-type="sheet" href="https://x.feishu.cn/sheets/TOK1">数据表</cite>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elems := result.TopBlocks[0].Text.Elements
	require.Len(t, elems, 2)
	md1 := elems[1].MentionDoc
	require.NotNil(t, md1)
	assert.Equal(t, "TOK1", md1.Token)
	assert.Equal(t, lark.DocxMentionObjTypeSheet, md1.ObjType)
	assert.Equal(t, "https%3A%2F%2Fx.feishu.cn%2Fsheets%2FTOK1", md1.URL) // 上传时 url-encode
	assert.Equal(t, "数据表", md1.Title)
	require.NotNil(t, md1.FallbackType)
	assert.Equal(t, "FallbackToLink", *md1.FallbackType)
}

// 裸飞书链接不升级：普通 [标题](url) 保持为超链接，不转 mention
func TestBareFeishuLinkNotUpgraded(t *testing.T) {
	md := `参考 [设计文档](https://x.feishu.cn/docx/TOK9)`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	for _, e := range result.TopBlocks[0].Text.Elements {
		assert.Nil(t, e.MentionDoc, "裸链接不应升级为 mention_doc")
		assert.Nil(t, e.MentionUser)
	}
	// 应作为带 Link 样式的文本存在
	var hasLink bool
	for _, e := range result.TopBlocks[0].Text.Elements {
		if e.TextRun != nil && e.TextRun.TextElementStyle != nil && e.TextRun.TextElementStyle.Link != nil {
			hasLink = true
		}
	}
	assert.True(t, hasLink, "应保留为普通超链接")
}

// 下载→上传 round-trip：parser 产出 <cite>，md2blocks 还原回等价 mention。
func TestMentionRoundTrip(t *testing.T) {
	docElem := &lark.DocxTextElementMentionDoc{
		Token:   "TokRT",
		ObjType: lark.DocxMentionObjTypeDocx,
		URL:     "https%3A%2F%2Fx.feishu.cn%2Fdocx%2FTokRT",
		Title:   "往返文档",
	}
	cite := renderMentionDoc(docElem)

	result, err := ConvertMarkdownToDocxBlocks(cite+"\n", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)
	back := result.TopBlocks[0].Text.Elements[0].MentionDoc
	require.NotNil(t, back)
	assert.Equal(t, docElem.Token, back.Token)
	assert.Equal(t, docElem.ObjType, back.ObjType)
	assert.Equal(t, docElem.URL, back.URL) // 编码 → 解码(href) → 编码 应一致
}
