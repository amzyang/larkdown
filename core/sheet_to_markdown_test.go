package core

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportSheetAsMarkdown(t *testing.T) {
	c := NewClient("test_app", "test_secret", nil)
	const spreadToken = "TestSpreadsheetTokenAbc"

	c.larkClient.Mock().MockDriveGetSheetList(func(ctx context.Context, req *lark.GetSheetListReq, opts ...lark.MethodOptionFunc) (*lark.GetSheetListResp, *lark.Response, error) {
		require.Equal(t, spreadToken, req.SpreadSheetToken)
		return &lark.GetSheetListResp{
			Sheets: []*lark.GetSheetListRespSheet{
				{SheetID: "s1", Title: "Simple", Index: 0},
				{SheetID: "s2", Title: "WithNewline", Index: 1},
				{SheetID: "s3", Title: "WithMerge", Index: 2},
				{SheetID: "s4", Title: "Hidden", Index: 3, Hidden: true},
				{SheetID: "s5", Title: "RichCells", Index: 4},
				{SheetID: "s6", Title: "FrozenHeader", Index: 5},
			},
		}, nil, nil
	})
	defer c.larkClient.Mock().UnMockDriveGetSheetList()

	str := func(s string) *string { return &s }
	cell := func(s string) lark.SheetContent { return lark.SheetContent{String: str(s)} }

	c.larkClient.Mock().MockDriveBatchGetSheetValue(func(ctx context.Context, req *lark.BatchGetSheetValueReq, opts ...lark.MethodOptionFunc) (*lark.BatchGetSheetValueResp, *lark.Response, error) {
		require.Equal(t, spreadToken, req.SpreadSheetToken)
		require.Len(t, req.Ranges, 1)
		var values [][]lark.SheetContent
		switch req.Ranges[0] {
		case "s1":
			values = [][]lark.SheetContent{
				{cell("A"), cell("B")},
				{cell("1"), cell("2")},
			}
		case "s2":
			values = [][]lark.SheetContent{
				{cell("Header"), cell("Note")},
				{cell("row1"), cell("line1\nline2")},
			}
		case "s3":
			values = [][]lark.SheetContent{
				{cell("Merged"), cell(""), cell("X")},
				{cell(""), cell(""), cell("Y")},
				{cell("a"), cell("b"), cell("c")},
			}
		case "s5":
			values = [][]lark.SheetContent{
				{cell("Type"), cell("Value")},
				{cell("link"), {Link: &lark.SheetValueLink{Text: "Feishu", Link: "https://feishu.cn/page"}}},
				{cell("at_doc"), {AtDoc: &lark.SheetValueAtDoc{Text: "doccnXyZ", ObjType: "docx"}}},
				{cell("at_user"), {AtUser: &lark.SheetValueAtUser{Text: "alice@example.com", TextType: "email"}}},
				{cell("embed_image"), {EmbedImage: &lark.SheetValueEmbedImage{Text: "screenshot.png", FileToken: "boxcnAAA"}}},
				{cell("attachment"), {Attachment: &lark.SheetValueAttachment{Text: "report.pdf", FileToken: "boxcnBBB"}}},
			}
		case "s6":
			values = [][]lark.SheetContent{
				{cell("Module"), cell("Type")},
				{cell("Module-zh"), cell("Type-zh")},
				{cell("login"), cell("event")},
				{cell("logout"), cell("event")},
			}
		default:
			t.Fatalf("unexpected sheet id: %s", req.Ranges[0])
		}
		return &lark.BatchGetSheetValueResp{
			ValueRanges: []*lark.BatchGetSheetValueRespValueRange{{Values: values}},
		}, nil, nil
	})
	defer c.larkClient.Mock().UnMockDriveBatchGetSheetValue()

	c.larkClient.Mock().MockDriveGetSheet(func(ctx context.Context, req *lark.GetSheetReq, opts ...lark.MethodOptionFunc) (*lark.GetSheetResp, *lark.Response, error) {
		var merges []*lark.GetSheetRespSheetMerge
		var grid *lark.GetSheetRespSheetGridProperties
		switch req.SheetID {
		case "s3":
			merges = []*lark.GetSheetRespSheetMerge{
				{StartRowIndex: 0, EndRowIndex: 1, StartColumnIndex: 0, EndColumnIndex: 1},
			}
		case "s1":
			grid = &lark.GetSheetRespSheetGridProperties{RowCount: 2, ColumnCount: 2}
		case "s6":
			grid = &lark.GetSheetRespSheetGridProperties{FrozenRowCount: 2, RowCount: 100, ColumnCount: 5}
		}
		return &lark.GetSheetResp{
			Sheet: &lark.GetSheetRespSheet{Merges: merges, GridProperties: grid},
		}, nil, nil
	})
	defer c.larkClient.Mock().UnMockDriveGetSheet()

	tmpDir := t.TempDir()
	const sourceURL = "https://example.feishu.cn/wiki/TestNodeToken"
	path, err := c.ExportSheetAsMarkdown(context.Background(), spreadToken, tmpDir, "TestSheet", sourceURL)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(path, "TestSheet.xlsx.md"), "文件名应以 .xlsx.md 结尾，path=%s", path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	md := string(data)

	assert.Contains(t, md, "# TestSheet", "应包含文档一级标题")
	assert.Contains(t, md, "## Simple", "应包含简单 tab")
	assert.Contains(t, md, "## WithNewline", "应包含多行 tab")
	assert.Contains(t, md, "## WithMerge", "应包含合并 tab")
	assert.NotContains(t, md, "## Hidden", "隐藏 tab 应被跳过")

	assert.Contains(t, md, "line1<br>line2", "多行单元格应使用 <br> 分隔")

	assert.Contains(t, md, "<table>", "有合并的 tab 应输出 HTML <table>")
	assert.Contains(t, md, `rowspan="2"`)
	assert.Contains(t, md, `colspan="2"`)

	assert.Contains(t, md, "| A | B |", "无合并的 tab 应输出 markdown 表格语法")
	assert.Contains(t, md, "BatchGetSheetValue", "应包含降级提示")

	assert.Contains(t, md, "<!--\nsource: "+sourceURL+"\n-->", "应在末尾写入 source frontmatter")
	fm, _, err := ParseFrontMatter(md)
	require.NoError(t, err)
	require.NotNil(t, fm, "ParseFrontMatter 应能识别 sheet 输出的 frontmatter")
	assert.Equal(t, sourceURL, fm.Source, "frontmatter source 应与传入一致")

	assert.Contains(t, md, "[Feishu](https://feishu.cn/page)", "Link cell 应渲染为 markdown 链接")
	assert.Contains(t, md, "[📄 doccnXyZ](https://feishu.cn/docx/doccnXyZ)", "AtDoc 应渲染为可点击链接")
	assert.Contains(t, md, "@alice@example.com", "AtUser 应有 @ 前缀")
	assert.Contains(t, md, "🖼️ screenshot.png", "EmbedImage 应有占位")
	assert.Contains(t, md, "📎 report.pdf", "Attachment 应有占位")

	assert.Contains(t, md, "> 2 行 × 2 列", "Simple tab 应输出大小元信息")
	assert.Contains(t, md, "> 100 行 × 5 列 · 前 2 行冻结", "FrozenHeader tab 应输出冻结说明")
	assert.Contains(t, md, "| Module<br>Module-zh | Type<br>Type-zh |", "前 2 行冻结时应折叠成多行表头")
}
