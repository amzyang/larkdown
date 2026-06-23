package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertParagraph(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("Hello **world**", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeText, block.BlockType)
	require.NotNil(t, block.Text)
	require.Len(t, block.Text.Elements, 2)
	assert.Equal(t, "Hello ", block.Text.Elements[0].TextRun.Content)
	assert.Equal(t, "world", block.Text.Elements[1].TextRun.Content)
	assert.True(t, block.Text.Elements[1].TextRun.TextElementStyle.Bold)
}

func TestConvertHeadings(t *testing.T) {
	md := "# H1\n\n## H2\n\n### H3\n\n#### H4\n\n##### H5\n\n###### H6"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 6)

	expected := []lark.DocxBlockType{
		lark.DocxBlockTypeHeading1,
		lark.DocxBlockTypeHeading2,
		lark.DocxBlockTypeHeading3,
		lark.DocxBlockTypeHeading4,
		lark.DocxBlockTypeHeading5,
		lark.DocxBlockTypeHeading6,
	}
	for i, block := range result.TopBlocks {
		assert.Equal(t, expected[i], block.BlockType, "heading level %d", i+1)
	}
	assert.Equal(t, "H1", result.TopBlocks[0].Heading1.Elements[0].TextRun.Content)
	assert.Equal(t, "H2", result.TopBlocks[1].Heading2.Elements[0].TextRun.Content)
}

func TestConvertBulletList(t *testing.T) {
	md := "- item1\n- item2\n- item3"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 3)

	for _, block := range result.TopBlocks {
		assert.Equal(t, lark.DocxBlockTypeBullet, block.BlockType)
		require.NotNil(t, block.Bullet)
	}
	assert.Equal(t, "item1", result.TopBlocks[0].Bullet.Elements[0].TextRun.Content)
	assert.Equal(t, "item2", result.TopBlocks[1].Bullet.Elements[0].TextRun.Content)
}

func TestConvertOrderedList(t *testing.T) {
	md := "1. first\n2. second\n3. third"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 3)

	for _, block := range result.TopBlocks {
		assert.Equal(t, lark.DocxBlockTypeOrdered, block.BlockType)
		require.NotNil(t, block.Ordered)
	}
	assert.Equal(t, "first", result.TopBlocks[0].Ordered.Elements[0].TextRun.Content)
}

func TestConvertNestedList(t *testing.T) {
	md := "- parent\n  - child1\n  - child2"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 嵌套列表应产生 DescendantGroup
	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]

	// 顶层应有 1 个 parent bullet
	require.Len(t, dg.ChildrenIDs, 1)

	// descendants 应包含 parent + 2 个 children
	require.GreaterOrEqual(t, len(dg.Descendants), 3)

	// 第一个 descendant 是 parent
	assert.Equal(t, lark.DocxBlockTypeBullet, dg.Descendants[0].BlockType)
	assert.Equal(t, "parent", dg.Descendants[0].Bullet.Elements[0].TextRun.Content)
	// parent 应有 2 个 children
	assert.Len(t, dg.Descendants[0].Children, 2)
}

func TestConvertTodoList(t *testing.T) {
	md := "- [x] done task\n- [ ] pending task"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 2)

	assert.Equal(t, lark.DocxBlockTypeTodo, result.TopBlocks[0].BlockType)
	assert.True(t, result.TopBlocks[0].Todo.Style.Done)
	assert.Equal(t, "done task", collectTextContent(result.TopBlocks[0].Todo.Elements))

	assert.Equal(t, lark.DocxBlockTypeTodo, result.TopBlocks[1].BlockType)
	assert.False(t, result.TopBlocks[1].Todo.Style.Done)
	assert.Equal(t, "pending task", collectTextContent(result.TopBlocks[1].Todo.Elements))
}

func TestConvertNestedTodoList(t *testing.T) {
	md := "- [ ] 1\n    - [ ] 2"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]

	require.Len(t, dg.ChildrenIDs, 1)
	require.GreaterOrEqual(t, len(dg.Descendants), 2)

	parent := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeTodo, parent.BlockType)
	assert.Equal(t, "1", collectTextContent(parent.Todo.Elements))
	assert.Len(t, parent.Children, 1)

	child := dg.Descendants[1]
	assert.Equal(t, lark.DocxBlockTypeTodo, child.BlockType)
	assert.Equal(t, "2", collectTextContent(child.Todo.Elements))
	assert.Equal(t, parent.Children[0], child.BlockID)
}

// collectTextContent 拼接所有 TextRun 的内容
func collectTextContent(elements []*lark.DocxTextElement) string {
	var buf strings.Builder
	for _, e := range elements {
		if e.TextRun != nil {
			buf.WriteString(e.TextRun.Content)
		}
		if e.Equation != nil {
			buf.WriteString(e.Equation.Content)
		}
	}
	return buf.String()
}

func TestConvertBulletWithCodeChild(t *testing.T) {
	// 列表项内含代码块（全缩进 → CommonMark 解析为该列表项的子块）。
	// 飞书原生支持 bullet→code 父子结构，上传应保留代码块为 bullet 的 child，
	// 而非（旧行为）静默丢弃。
	md := "- bar\n\n  ```bash\n  echo \"hello world\"\n  ```\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 应产生 DescendantGroup：bullet 顶层 + code child
	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	require.Len(t, dg.ChildrenIDs, 1)
	require.GreaterOrEqual(t, len(dg.Descendants), 2)

	bullet := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeBullet, bullet.BlockType)
	assert.Equal(t, "bar", collectTextContent(bullet.Bullet.Elements))
	require.Len(t, bullet.Children, 1)

	code := dg.Descendants[1]
	assert.Equal(t, lark.DocxBlockTypeCode, code.BlockType)
	assert.Equal(t, bullet.Children[0], code.BlockID)
	assert.Equal(t, "echo \"hello world\"", collectTextContent(code.Code.Elements))
	assert.Equal(t, lark.DocxCodeLanguageBash, code.Code.Style.Language)
}

func TestConvertBulletWithSecondParagraph(t *testing.T) {
	// 列表项内第二个段落 → 作为 bullet 的 text child（旧行为：丢弃）
	md := "- bar\n\n  second para\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	require.Len(t, dg.ChildrenIDs, 1)
	require.GreaterOrEqual(t, len(dg.Descendants), 2)

	bullet := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeBullet, bullet.BlockType)
	assert.Equal(t, "bar", collectTextContent(bullet.Bullet.Elements))
	require.Len(t, bullet.Children, 1)

	second := dg.Descendants[1]
	assert.Equal(t, lark.DocxBlockTypeText, second.BlockType)
	assert.Equal(t, bullet.Children[0], second.BlockID)
	assert.Equal(t, "second para", collectTextContent(second.Text.Elements))
}

func TestConvertBulletWithDivider(t *testing.T) {
	// 列表项内分隔线 → 作为 bullet 的 divider child
	md := "- bar\n\n  ---\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	bullet := dg.Descendants[0]
	require.Len(t, bullet.Children, 1)

	div := dg.Descendants[1]
	assert.Equal(t, lark.DocxBlockTypeDivider, div.BlockType)
	assert.Equal(t, bullet.Children[0], div.BlockID)
}

func TestConvertBulletWithImageChild(t *testing.T) {
	// 列表项内独立成段的图片 → 作为 bullet 的 image child（记录到 DescendantImages）
	md := "- bar\n\n  ![alt](img.png)\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	require.Len(t, dg.ChildrenIDs, 1)
	require.GreaterOrEqual(t, len(dg.Descendants), 2)

	bullet := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeBullet, bullet.BlockType)
	assert.Equal(t, "bar", collectTextContent(bullet.Bullet.Elements))
	require.Len(t, bullet.Children, 1)

	img := dg.Descendants[1]
	assert.Equal(t, lark.DocxBlockTypeImage, img.BlockType)
	assert.Equal(t, bullet.Children[0], img.BlockID)

	require.Len(t, dg.DescendantImages, 1)
	assert.Equal(t, "img.png", dg.DescendantImages[0].ImagePath)
	assert.Equal(t, img.BlockID, dg.DescendantImages[0].TempBlockID)
}

func TestConvertBulletWithImageFirst(t *testing.T) {
	// 列表项首段即图片（- ![](x)）→ bullet 本体空 + image child，图片不丢
	md := "- ![alt](img.png)\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	bullet := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeBullet, bullet.BlockType)
	require.Len(t, bullet.Children, 1)

	img := dg.Descendants[1]
	assert.Equal(t, lark.DocxBlockTypeImage, img.BlockType)
	require.Len(t, dg.DescendantImages, 1)
	assert.Equal(t, "img.png", dg.DescendantImages[0].ImagePath)
}

func TestConvertBulletWithQuoteChild(t *testing.T) {
	// 列表项内引用块 → 作为 bullet 的 quote_container child
	md := "- bar\n\n  > quoted text\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	bullet := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeBullet, bullet.BlockType)
	require.Len(t, bullet.Children, 1)

	var quote *lark.DocxBlock
	for _, d := range dg.Descendants {
		if d.BlockID == bullet.Children[0] {
			quote = d
		}
	}
	require.NotNil(t, quote)
	assert.Equal(t, lark.DocxBlockTypeQuoteContainer, quote.BlockType)
	require.Len(t, quote.Children, 1) // quote 内的 text 子块
}

func TestConvertBulletWithTableChild(t *testing.T) {
	// 列表项内表格 → 作为 bullet 的 table child
	md := "- bar\n\n  | a | b |\n  | --- | --- |\n  | 1 | 2 |\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	bullet := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeBullet, bullet.BlockType)
	require.Len(t, bullet.Children, 1)

	var table *lark.DocxBlock
	for _, d := range dg.Descendants {
		if d.BlockID == bullet.Children[0] {
			table = d
		}
	}
	require.NotNil(t, table)
	assert.Equal(t, lark.DocxBlockTypeTable, table.BlockType)
	assert.Equal(t, 4, len(table.Children)) // 2x2 = 4 cells
}

func TestConvertCodeBlock(t *testing.T) {
	md := "```go\nfunc main() {}\n```"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeCode, block.BlockType)
	require.NotNil(t, block.Code)
	assert.Equal(t, lark.DocxCodeLanguageGo, block.Code.Style.Language)
	assert.Equal(t, "func main() {}", block.Code.Elements[0].TextRun.Content)
}

func TestConvertCodeBlockAlias(t *testing.T) {
	md := "```js\nconsole.log('hi')\n```"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)
	assert.Equal(t, lark.DocxCodeLanguageJavaScript, result.TopBlocks[0].Code.Style.Language)
}

func TestConvertDivider(t *testing.T) {
	md := "above\n\n---\n\nbelow"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 3)
	assert.Equal(t, lark.DocxBlockTypeDivider, result.TopBlocks[1].BlockType)
}

func TestConvertImage(t *testing.T) {
	md := "![alt](image.png)"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeImage, block.BlockType)
	require.NotNil(t, block.Image)
	assert.Equal(t, "", block.Image.Token) // 空 token，后续上传填充

	require.Len(t, result.ImageIndices, 1)
	assert.Equal(t, 0, result.ImageIndices[0])
}

func TestConvertMermaid(t *testing.T) {
	md := "```mermaid\ngraph TD\n  A --> B\n```"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeAddOns, block.BlockType)
	require.NotNil(t, block.AddOns)
	assert.Equal(t, addOnsMermaidComponentTypeID, block.AddOns.ComponentTypeID)
	assert.Contains(t, block.AddOns.Record, "graph TD")
}

func TestConvertPlantUML(t *testing.T) {
	md := "```plantuml\n@startuml\nA -> B: hello\n@enduml\n```"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeBoard, block.BlockType)
	require.NotNil(t, block.Board)

	// BoardIndices/BoardCodes 应正确填充
	require.Len(t, result.BoardIndices, 1)
	assert.Equal(t, 0, result.BoardIndices[0])
	require.Len(t, result.BoardCodes, 1)
	assert.Contains(t, result.BoardCodes[0], "@startuml")
	assert.Contains(t, result.BoardCodes[0], "A -> B: hello")

	// 不应被标记为图片
	assert.Empty(t, result.ImageIndices)
}

func TestConvertPlantUMLCaseInsensitive(t *testing.T) {
	md := "```PlantUML\n@startuml\nA -> B\n@enduml\n```"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)
	assert.Equal(t, lark.DocxBlockTypeBoard, result.TopBlocks[0].BlockType)
}

func TestConvertBlockMath(t *testing.T) {
	md := "$$\nE=mc^2\n$$"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeText, block.BlockType)
	require.NotNil(t, block.Text)
	require.Len(t, block.Text.Elements, 1)
	require.NotNil(t, block.Text.Elements[0].Equation)
	assert.Equal(t, "E=mc^2", block.Text.Elements[0].Equation.Content)
}

func TestConvertInlineMath(t *testing.T) {
	md := "The formula $E=mc^2$ is famous."
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeText, block.BlockType)

	// 应该有 3 个元素: "The formula ", equation, " is famous."
	found := false
	for _, elem := range block.Text.Elements {
		if elem.Equation != nil {
			assert.Equal(t, "E=mc^2", elem.Equation.Content)
			found = true
		}
	}
	assert.True(t, found, "should contain inline equation")
}

func TestConvertMathStrictMode(t *testing.T) {
	// $100 不应被识别为公式（前面无空白且后面无闭合 $）
	md := "The price is $100 for this item."
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	for _, elem := range block.Text.Elements {
		assert.Nil(t, elem.Equation, "should not have equation for $100")
	}
}

func TestConvertCallout(t *testing.T) {
	md := "> [!NOTE]\n> This is a note."
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]

	// 第一个 descendant 应该是 Callout
	require.GreaterOrEqual(t, len(dg.Descendants), 1)
	callout := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeCallout, callout.BlockType)
	require.NotNil(t, callout.Callout)
	assert.Equal(t, lark.DocxCalloutBackgroundColorLightBlue, callout.Callout.BackgroundColor)
}

func TestConvertBlockquote(t *testing.T) {
	md := "> This is a quote."
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	require.GreaterOrEqual(t, len(dg.Descendants), 1)
	quote := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeQuoteContainer, quote.BlockType)
}

func TestConvertTable(t *testing.T) {
	md := "| a | b |\n| --- | --- |\n| 1 | 2 |"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]

	// 第一个 descendant 应该是 Table
	require.GreaterOrEqual(t, len(dg.Descendants), 1)
	table := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeTable, table.BlockType)
	require.NotNil(t, table.Table)
	assert.Equal(t, int64(2), table.Table.Property.RowSize)
	assert.Equal(t, int64(2), table.Table.Property.ColumnSize)
}

// Inline style tests

func TestInlineBold(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("**bold text**", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elem := result.TopBlocks[0].Text.Elements[0]
	assert.Equal(t, "bold text", elem.TextRun.Content)
	assert.True(t, elem.TextRun.TextElementStyle.Bold)
}

func TestInlineItalic(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("*italic text*", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elem := result.TopBlocks[0].Text.Elements[0]
	assert.Equal(t, "italic text", elem.TextRun.Content)
	assert.True(t, elem.TextRun.TextElementStyle.Italic)
}

func TestInlineBoldItalic(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("***bold italic***", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elem := result.TopBlocks[0].Text.Elements[0]
	assert.Equal(t, "bold italic", elem.TextRun.Content)
	assert.True(t, elem.TextRun.TextElementStyle.Bold)
	assert.True(t, elem.TextRun.TextElementStyle.Italic)
}

func TestInlineCode(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("use `fmt.Println`", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	require.Len(t, elements, 2)
	assert.Equal(t, "fmt.Println", elements[1].TextRun.Content)
	assert.True(t, elements[1].TextRun.TextElementStyle.InlineCode)
}

func TestInlineStrikethrough(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("~~deleted~~", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elem := result.TopBlocks[0].Text.Elements[0]
	assert.Equal(t, "deleted", elem.TextRun.Content)
	assert.True(t, elem.TextRun.TextElementStyle.Strikethrough)
}

func TestInlineLink(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("[Google](https://google.com)", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elem := result.TopBlocks[0].Text.Elements[0]
	assert.Equal(t, "Google", elem.TextRun.Content)
	require.NotNil(t, elem.TextRun.TextElementStyle.Link)
	assert.Equal(t, "https://google.com", elem.TextRun.TextElementStyle.Link.URL)
}

func TestIsValidLinkURL(t *testing.T) {
	valid := []string{"https://x.com", "http://x.com/a?b=1", "mailto:a@b.c", "tel:+8610086"}
	for _, u := range valid {
		assert.True(t, isValidLinkURL(u), u)
	}
	invalid := []string{"./TECH.md", "TECH.md", "../docs/a.md", "#anchor", "/abs/path", ""}
	for _, u := range invalid {
		assert.False(t, isValidLinkURL(u), u)
	}
}

func TestInlineLinkInvalidFallback(t *testing.T) {
	// 段落内相对链接（文件不存在，不走 file 块路径）→ 降级纯文本
	result, err := ConvertMarkdownToDocxBlocks("[TECH.md](./not-exist.md)", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)
	elem := result.TopBlocks[0].Text.Elements[0]
	assert.Equal(t, "TECH.md", elem.TextRun.Content)
	assert.Nil(t, elem.TextRun.TextElementStyle.Link)

	// HTML <a> 相对链接 → 降级纯文本
	result, err = ConvertMarkdownToDocxBlocks(`<a href="./x.md">文字</a>`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)
	elem = result.TopBlocks[0].Text.Elements[0]
	assert.Equal(t, "文字", elem.TextRun.Content)
	assert.Nil(t, elem.TextRun.TextElementStyle.Link)
}

func TestBlockquoteRelativeLinkFallback(t *testing.T) {
	// 复现 1770006：blockquote 内相对链接被原样写入 link.url 被飞书拒绝
	result, err := ConvertMarkdownToDocxBlocks("> 配套技术实现见同目录 [TECH.md](./TECH.md)。", "")
	require.NoError(t, err)
	require.Len(t, result.DescendantGroups, 1)

	dg := result.DescendantGroups[0]
	require.GreaterOrEqual(t, len(dg.Descendants), 2)
	assert.Equal(t, lark.DocxBlockTypeQuoteContainer, dg.Descendants[0].BlockType)

	text := dg.Descendants[1]
	require.NotNil(t, text.Text)
	var linkText *lark.DocxTextElement
	for _, e := range text.Text.Elements {
		if e.TextRun != nil && e.TextRun.Content == "TECH.md" {
			linkText = e
		}
	}
	require.NotNil(t, linkText)
	assert.Nil(t, linkText.TextRun.TextElementStyle.Link)
}

func TestInlineNestedStyles(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("**[`code`](https://example.com)**", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	require.GreaterOrEqual(t, len(elements), 1)

	// 应该有 bold + link + inline_code
	elem := elements[0]
	assert.Equal(t, "code", elem.TextRun.Content)
	assert.True(t, elem.TextRun.TextElementStyle.Bold)
	assert.True(t, elem.TextRun.TextElementStyle.InlineCode)
	require.NotNil(t, elem.TextRun.TextElementStyle.Link)
	assert.Equal(t, "https://example.com", elem.TextRun.TextElementStyle.Link.URL)
}

// Language mapping tests

func TestMdStr2DocxCodeLang(t *testing.T) {
	tests := map[string]lark.DocxCodeLanguage{
		"go":         lark.DocxCodeLanguageGo,
		"python":     lark.DocxCodeLanguagePython,
		"javascript": lark.DocxCodeLanguageJavaScript,
		"typescript": lark.DocxCodeLanguageTypeScript,
		"rust":       lark.DocxCodeLanguageRust,
		"java":       lark.DocxCodeLanguageJava,
		// aliases
		"js":     lark.DocxCodeLanguageJavaScript,
		"ts":     lark.DocxCodeLanguageTypeScript,
		"py":     lark.DocxCodeLanguagePython,
		"sh":     lark.DocxCodeLanguageBash,
		"golang": lark.DocxCodeLanguageGo,
		"rs":     lark.DocxCodeLanguageRust,
	}
	for md, expected := range tests {
		got, ok := MdStr2DocxCodeLang[md]
		assert.True(t, ok, "should have mapping for %q", md)
		assert.Equal(t, expected, got, "for %q", md)
	}
}

func TestMdStr2DocxCodeLangUnknown(t *testing.T) {
	_, ok := MdStr2DocxCodeLang["nonexistent_language"]
	assert.False(t, ok)
}

// Callout color mapping tests

func TestAlertTypeToCalloutColor(t *testing.T) {
	tests := map[string]lark.DocxCalloutBackgroundColor{
		"CAUTION":   lark.DocxCalloutBackgroundColorLightRed,
		"WARNING":   lark.DocxCalloutBackgroundColorLightOrange,
		"TIP":       lark.DocxCalloutBackgroundColorLightGreen,
		"NOTE":      lark.DocxCalloutBackgroundColorLightBlue,
		"IMPORTANT": lark.DocxCalloutBackgroundColorLightPurple,
	}
	for alertType, expected := range tests {
		got, ok := alertTypeToCalloutColor[alertType]
		assert.True(t, ok, "should have mapping for %q", alertType)
		assert.Equal(t, expected, got, "for %q", alertType)
	}
}

// Complex document test

func TestConvertComplexDocument(t *testing.T) {
	md := `## Introduction

This is a **paragraph** with *inline* styles.

- bullet 1
- bullet 2

1. ordered 1
2. ordered 2

---

` + "```python\nprint('hello')\n```" + `

| Name | Age |
| --- | --- |
| Alice | 30 |

> A simple quote.

> [!WARNING]
> Be careful!

- [x] task done
- [ ] task pending
`

	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 应该包含多种块类型
	assert.Greater(t, len(result.TopBlocks), 5)
	assert.Greater(t, len(result.DescendantGroups), 0) // table + quote + callout

	// 验证第一个块是 Heading2
	assert.Equal(t, lark.DocxBlockTypeHeading2, result.TopBlocks[0].BlockType)
}

func TestConvertDetails(t *testing.T) {
	md := "<details>\n<summary>折叠标题</summary>\n\n- item 1\n- item 2\n\n</details>"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// TopBlocks 只有 1 个 nil placeholder
	require.Len(t, result.TopBlocks, 1)
	assert.Nil(t, result.TopBlocks[0])

	// 1 个 DescendantGroup
	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	assert.Equal(t, 0, dg.TopBlockIndex)

	// 第一个 descendant 是 Text(folded=true) 父块
	parent := dg.Descendants[0]
	assert.Equal(t, lark.DocxBlockTypeText, parent.BlockType)
	require.NotNil(t, parent.Text)
	require.NotNil(t, parent.Text.Style)
	assert.True(t, parent.Text.Style.Folded)
	assert.Equal(t, "折叠标题", collectTextContent(parent.Text.Elements))
	assert.Len(t, parent.Children, 2)

	// 2 个 Bullet 子块
	assert.Equal(t, lark.DocxBlockTypeBullet, dg.Descendants[1].BlockType)
	assert.Equal(t, lark.DocxBlockTypeBullet, dg.Descendants[2].BlockType)
}

func TestConvertDetailsNoSummary(t *testing.T) {
	md := "<details>\n\ncontent\n\n</details>"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	// <details> without <summary> → generic HTML, no folded block
	// </details> without active capture → ignored
	// 只剩 content text block
	hasText := false
	for _, b := range result.TopBlocks {
		if b != nil && b.BlockType == lark.DocxBlockTypeText {
			hasText = true
		}
	}
	assert.True(t, hasText, "should have text block for content")
	assert.Len(t, result.DescendantGroups, 0, "no descendant groups for details without summary")
}

func TestConvertDetailsWithCode(t *testing.T) {
	md := "<details>\n<summary>代码示例</summary>\n\n```json\n{\"key\": \"value\"}\n```\n\n</details>"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	parent := dg.Descendants[0]
	assert.True(t, parent.Text.Style.Folded)
	assert.Equal(t, "代码示例", collectTextContent(parent.Text.Elements))
	assert.Len(t, parent.Children, 1)
	assert.Equal(t, lark.DocxBlockTypeCode, dg.Descendants[1].BlockType)
}

func TestConvertDetailsNested(t *testing.T) {
	md := "<details>\n<summary>外层</summary>\n\n文本内容\n\n<details>\n<summary>内层</summary>\n\n- item\n\n</details>\n\n</details>"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 外层 details → 1 个 DescendantGroup
	require.Len(t, result.DescendantGroups, 1)
	dg := result.DescendantGroups[0]
	parent := dg.Descendants[0]
	assert.True(t, parent.Text.Style.Folded)
	assert.Equal(t, "外层", collectTextContent(parent.Text.Elements))
	// 子块: 文本 + 内层 details (其 ChildrenIDs 被合并)
	assert.GreaterOrEqual(t, len(parent.Children), 2)
}

func TestConvertEmptyDocument(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("", "")
	require.NoError(t, err)
	assert.Len(t, result.TopBlocks, 0)
	assert.Len(t, result.DescendantGroups, 0)
	assert.Len(t, result.ImageIndices, 0)
}

func TestConvertMultipleImages(t *testing.T) {
	md := "![img1](a.png)\n\n![img2](b.png)\n\nsome text"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	assert.Len(t, result.ImageIndices, 2)
	assert.Equal(t, 0, result.ImageIndices[0])
	assert.Equal(t, 1, result.ImageIndices[1])
	assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[0].BlockType)
	assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[1].BlockType)
	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[2].BlockType)
}

func TestConvertCalloutTypes(t *testing.T) {
	types := []string{"NOTE", "TIP", "WARNING", "CAUTION", "IMPORTANT"}
	for _, alertType := range types {
		md := "> [!" + alertType + "]\n> Content here."
		result, err := ConvertMarkdownToDocxBlocks(md, "")
		require.NoError(t, err, "for %s", alertType)
		require.Len(t, result.DescendantGroups, 1, "for %s", alertType)

		callout := result.DescendantGroups[0].Descendants[0]
		assert.Equal(t, lark.DocxBlockTypeCallout, callout.BlockType, "for %s", alertType)
		assert.Equal(t, alertTypeToCalloutColor[alertType], callout.Callout.BackgroundColor, "for %s", alertType)
	}
}

// Bug 1 回归测试: $$ 关闭行后的内容不应被吞入公式块
func TestBlockMathWithContentAfter(t *testing.T) {
	md := "$$\nE=mc^2\n$$\n\n## Heading After\n\nSome text here."
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 3, "should have equation + heading + text")

	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
	require.NotNil(t, result.TopBlocks[0].Text)
	require.NotNil(t, result.TopBlocks[0].Text.Elements[0].Equation)
	assert.Equal(t, "E=mc^2", result.TopBlocks[0].Text.Elements[0].Equation.Content)

	assert.Equal(t, lark.DocxBlockTypeHeading2, result.TopBlocks[1].BlockType)
	assert.Equal(t, "Heading After", collectTextContent(result.TopBlocks[1].Heading2.Elements))

	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[2].BlockType)
	assert.Equal(t, "Some text here.", collectTextContent(result.TopBlocks[2].Text.Elements))
}

// Bug 3 回归测试: CJK 字符后紧跟 $ 应正确识别行内公式
func TestInlineMathCJK(t *testing.T) {
	md := "行内公式：$E=mc^2$ 是著名的。"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	block := result.TopBlocks[0]
	assert.Equal(t, lark.DocxBlockTypeText, block.BlockType)

	found := false
	for _, elem := range block.Text.Elements {
		if elem.Equation != nil {
			assert.Equal(t, "E=mc^2", elem.Equation.Content)
			found = true
		}
	}
	assert.True(t, found, "should contain inline equation after CJK text")
}

// DocxBlock JSON 序列化测试: 确保 omitempty 修复后不会包含多余的零值字段
func TestDocxBlockJSON(t *testing.T) {
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockTypeText,
		Text: &lark.DocxBlockText{
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: "test"},
			}},
		},
	}
	data, err := json.Marshal(block)
	require.NoError(t, err)

	jsonStr := string(data)
	assert.NotContains(t, jsonStr, `"divider"`, "should not contain divider field for text block")
	assert.NotContains(t, jsonStr, `"add_ons"`, "should not contain add_ons field for text block")
	assert.Contains(t, jsonStr, `"text"`, "should contain text field")
}

// TestConvertFromFile 数据驱动的集成测试，每个 testdata 文件测试单一 Markdown 元素的转换能力
func TestConvertFromFile(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		validate func(t *testing.T, result *ConvertResult)
	}{
		{
			name: "inline_styles",
			file: "../testdata/upload.inline_styles.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 文件有 2 个段落
				require.Len(t, result.TopBlocks, 2)
				for _, block := range result.TopBlocks {
					assert.Equal(t, lark.DocxBlockTypeText, block.BlockType)
					styles := map[string]bool{}
					for _, e := range block.Text.Elements {
						if e.TextRun != nil {
							s := e.TextRun.TextElementStyle
							if s.Bold {
								styles["bold"] = true
							}
							if s.Italic {
								styles["italic"] = true
							}
							if s.Strikethrough {
								styles["strikethrough"] = true
							}
							if s.InlineCode {
								styles["inline_code"] = true
							}
						}
					}
					assert.True(t, styles["bold"], "should contain bold")
					assert.True(t, styles["italic"], "should contain italic")
					assert.True(t, styles["strikethrough"], "should contain strikethrough")
					assert.True(t, styles["inline_code"], "should contain inline_code")
				}
			},
		},
		{
			name: "link",
			file: "../testdata/upload.link.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 1)
				block := result.TopBlocks[0]
				assert.Equal(t, lark.DocxBlockTypeText, block.BlockType)
				found := false
				for _, e := range block.Text.Elements {
					if e.TextRun != nil && e.TextRun.TextElementStyle.Link != nil {
						assert.Equal(t, "飞书开放平台", e.TextRun.Content)
						assert.Equal(t, "https://open.feishu.cn/", e.TextRun.TextElementStyle.Link.URL)
						found = true
					}
				}
				assert.True(t, found, "should contain a link element")
			},
		},
		{
			name: "empty_link",
			file: "../testdata/upload.empty_link.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 1)
				block := result.TopBlocks[0]
				assert.Equal(t, lark.DocxBlockTypeText, block.BlockType)

				// 空 dest 的 markdown link 退化为纯文本：飞书 descendant 接口拒收
				// 空 URL（link.url is required），因此不挂 Link。
				var found *lark.DocxTextElement
				for _, e := range block.Text.Elements {
					if e.TextRun != nil && strings.Contains(e.TextRun.Content, "文本链接") {
						found = e
						break
					}
				}
				require.NotNil(t, found, "should find text content '文本链接'")
				if found.TextRun.TextElementStyle != nil {
					assert.Nil(t, found.TextRun.TextElementStyle.Link,
						"empty-dest link should not carry Link style")
				}
			},
		},
		{
			name: "image",
			file: "../testdata/upload.image.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 7)
				// 4 image blocks at indices 0, 2, 4, 6
				for _, i := range []int{0, 2, 4, 6} {
					assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[i].BlockType)
					require.NotNil(t, result.TopBlocks[i].Image)
				}
				// 3 space-text blocks at indices 1, 3, 5
				for _, i := range []int{1, 3, 5} {
					assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[i].BlockType)
				}
				require.Len(t, result.ImageIndices, 4)
				assert.Equal(t, []int{0, 2, 4, 6}, result.ImageIndices)
				assert.Equal(t, []string{"clock.png", "8.png", "9.png", "10.png"}, result.ImagePaths)
			},
		},
		{
			name: "heading",
			file: "../testdata/upload.heading.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// H3, text, H4, text = 4 blocks
				require.Len(t, result.TopBlocks, 4)
				assert.Equal(t, lark.DocxBlockTypeHeading3, result.TopBlocks[0].BlockType)
				assert.Equal(t, "三级标题", collectTextContent(result.TopBlocks[0].Heading3.Elements))
				assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[1].BlockType)
				assert.Equal(t, lark.DocxBlockTypeHeading4, result.TopBlocks[2].BlockType)
				assert.Equal(t, "四级标题", collectTextContent(result.TopBlocks[2].Heading4.Elements))
				assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[3].BlockType)
			},
		},
		{
			name: "bullet_list",
			file: "../testdata/upload.bullet_list.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 顶层 3 项各占独立槽位（与飞书 N 个顶层块对齐）：
				// 第一项(无子项)→普通块, 第二项(含子项)→DescendantGroup, 第三项(无子项)→普通块
				require.Len(t, result.TopBlocks, 3)
				assert.Equal(t, lark.DocxBlockTypeBullet, result.TopBlocks[0].BlockType)
				assert.Nil(t, result.TopBlocks[1]) // 第二项是 group 占位
				assert.Equal(t, lark.DocxBlockTypeBullet, result.TopBlocks[2].BlockType)
				require.Len(t, result.DescendantGroups, 1)
				dg := result.DescendantGroups[0]
				assert.Equal(t, 1, dg.TopBlockIndex)
				require.Len(t, dg.ChildrenIDs, 1) // 单根（第二项）
				// 第二项子树: 第二项 + 2 个嵌套 = 3 块，均为 Bullet
				assert.Len(t, dg.Descendants, 3)
				for _, d := range dg.Descendants {
					assert.Equal(t, lark.DocxBlockTypeBullet, d.BlockType)
				}
			},
		},
		{
			name: "ordered_list",
			file: "../testdata/upload.ordered_list.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 顶层 3 项各占独立槽位：步骤一/二(无子项)→普通块, 步骤三(含子项)→DescendantGroup
				require.Len(t, result.TopBlocks, 3)
				assert.Equal(t, lark.DocxBlockTypeOrdered, result.TopBlocks[0].BlockType)
				assert.Equal(t, lark.DocxBlockTypeOrdered, result.TopBlocks[1].BlockType)
				assert.Nil(t, result.TopBlocks[2]) // 步骤三是 group 占位
				require.Len(t, result.DescendantGroups, 1)
				dg := result.DescendantGroups[0]
				assert.Equal(t, 2, dg.TopBlockIndex)
				require.Len(t, dg.ChildrenIDs, 1) // 单根（步骤三）
				// 步骤三子树: 步骤三 + 3 个嵌套 = 4 块，均为 Ordered
				assert.Len(t, dg.Descendants, 4)
				for _, d := range dg.Descendants {
					assert.Equal(t, lark.DocxBlockTypeOrdered, d.BlockType)
				}
			},
		},
		{
			name: "todo_list",
			file: "../testdata/upload.todo_list.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 4)
				for _, block := range result.TopBlocks {
					assert.Equal(t, lark.DocxBlockTypeTodo, block.BlockType)
					require.NotNil(t, block.Todo)
				}
				// 前两个已完成
				assert.True(t, result.TopBlocks[0].Todo.Style.Done)
				assert.True(t, result.TopBlocks[1].Todo.Style.Done)
				// 后两个未完成
				assert.False(t, result.TopBlocks[2].Todo.Style.Done)
				assert.False(t, result.TopBlocks[3].Todo.Style.Done)
			},
		},
		{
			name: "code_block",
			file: "../testdata/upload.code_block.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// "Go 示例：" text + go code + "Python 示例：" text + python code + "JSON 配置示例：" text + json code
				codeBlocks := []*lark.DocxBlock{}
				for _, b := range result.TopBlocks {
					if b.BlockType == lark.DocxBlockTypeCode {
						codeBlocks = append(codeBlocks, b)
					}
				}
				require.Len(t, codeBlocks, 3)
				assert.Equal(t, lark.DocxCodeLanguageGo, codeBlocks[0].Code.Style.Language)
				assert.Equal(t, lark.DocxCodeLanguagePython, codeBlocks[1].Code.Style.Language)
				assert.Equal(t, lark.DocxCodeLanguageJSON, codeBlocks[2].Code.Style.Language)
			},
		},
		{
			name: "table",
			file: "../testdata/upload.table.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.DescendantGroups, 1)
				dg := result.DescendantGroups[0]
				require.GreaterOrEqual(t, len(dg.Descendants), 1)
				table := dg.Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeTable, table.BlockType)
				require.NotNil(t, table.Table)
				assert.Equal(t, int64(11), table.Table.Property.RowSize)    // header + 10 data rows
				assert.Equal(t, int64(10), table.Table.Property.ColumnSize) // 10 columns
			},
		},
		{
			name: "blockquote",
			file: "../testdata/upload.blockquote.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 两个引用块 → 2 个 DescendantGroup
				require.Len(t, result.DescendantGroups, 2)
				// 第一个是简单引用
				quote1 := result.DescendantGroups[0].Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeQuoteContainer, quote1.BlockType)
				// 第二个也是引用（嵌套引用）
				quote2 := result.DescendantGroups[1].Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeQuoteContainer, quote2.BlockType)
			},
		},
		{
			name: "blockquote_rich",
			file: "../testdata/upload.blockquote_rich.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 4 个引用块 → 4 个 DescendantGroup
				require.Len(t, result.DescendantGroups, 4)

				// 第 1 个：含图片的引用 → text + image 子块
				q1 := result.DescendantGroups[0].Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeQuoteContainer, q1.BlockType)
				assert.Len(t, q1.Children, 2) // 文字段落 + 图片块
				// 第 2 个 descendant 应是 Image
				q1img := result.DescendantGroups[0].Descendants[2]
				assert.Equal(t, lark.DocxBlockTypeImage, q1img.BlockType)
				require.Len(t, result.DescendantGroups[0].DescendantImages, 1)
				assert.Equal(t, "clock.png", result.DescendantGroups[0].DescendantImages[0].ImagePath)

				// 第 2 个：图文混排引用 → text + image + text (3 个子块)
				q2 := result.DescendantGroups[1].Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeQuoteContainer, q2.BlockType)
				assert.Len(t, q2.Children, 3)
				// 验证拆分结构：text, image, text
				q2d := result.DescendantGroups[1].Descendants
				assert.Equal(t, lark.DocxBlockTypeText, q2d[1].BlockType)
				assert.Equal(t, lark.DocxBlockTypeImage, q2d[2].BlockType)
				assert.Equal(t, lark.DocxBlockTypeText, q2d[3].BlockType)
				require.Len(t, result.DescendantGroups[1].DescendantImages, 1)
				assert.Equal(t, "8.png", result.DescendantGroups[1].DescendantImages[0].ImagePath)

				// 第 3 个：inline 样式引用
				q3 := result.DescendantGroups[2].Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeQuoteContainer, q3.BlockType)

				// 第 4 个：含代码块的引用
				q4 := result.DescendantGroups[3].Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeQuoteContainer, q4.BlockType)
				// 应有 2 个子块：文字段落 + 代码块
				assert.Len(t, q4.Children, 2)
				q4code := result.DescendantGroups[3].Descendants[2]
				assert.Equal(t, lark.DocxBlockTypeCode, q4code.BlockType)
			},
		},
		{
			name: "divider",
			file: "../testdata/upload.divider.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 3)
				assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
				assert.Equal(t, lark.DocxBlockTypeDivider, result.TopBlocks[1].BlockType)
				assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[2].BlockType)
			},
		},
		{
			name: "math",
			file: "../testdata/upload.math.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 行内公式段落 + 块级公式段落前的空行/"块级公式：" text + equation block
				hasInlineEquation := false
				hasBlockEquation := false
				for _, b := range result.TopBlocks {
					if b.BlockType != lark.DocxBlockTypeText || b.Text == nil {
						continue
					}
					for _, e := range b.Text.Elements {
						if e.Equation != nil {
							content := e.Equation.Content
							if content == "E = mc^2" {
								hasInlineEquation = true
							}
							if strings.Contains(content, `\sum`) {
								hasBlockEquation = true
							}
						}
					}
				}
				assert.True(t, hasInlineEquation, "should have inline equation E = mc^2")
				assert.True(t, hasBlockEquation, "should have block equation with \\sum")
			},
		},
		{
			name: "callout",
			file: "../testdata/upload.callout.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.DescendantGroups, 3)
				expected := []lark.DocxCalloutBackgroundColor{
					lark.DocxCalloutBackgroundColorLightBlue,   // NOTE
					lark.DocxCalloutBackgroundColorLightOrange, // WARNING
					lark.DocxCalloutBackgroundColorLightGreen,  // TIP
				}
				for i, dg := range result.DescendantGroups {
					callout := dg.Descendants[0]
					assert.Equal(t, lark.DocxBlockTypeCallout, callout.BlockType, "callout %d", i)
					require.NotNil(t, callout.Callout, "callout %d", i)
					assert.Equal(t, expected[i], callout.Callout.BackgroundColor, "callout %d color", i)
				}
			},
		},
		{
			name: "details",
			file: "../testdata/upload.details.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 2 个 <details> → 2 个 DescendantGroup（各含 folded Text 父块 + children）
				require.Len(t, result.DescendantGroups, 2)

				// 第一个 details: Text(folded) + 5 Bullet
				dg1 := result.DescendantGroups[0]
				parent1 := dg1.Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeText, parent1.BlockType)
				require.NotNil(t, parent1.Text)
				require.NotNil(t, parent1.Text.Style)
				assert.True(t, parent1.Text.Style.Folded)
				assert.Contains(t, collectTextContent(parent1.Text.Elements), "larkdown 支持的文档类型")
				assert.Len(t, parent1.Children, 5)
				for i := 1; i <= 5; i++ {
					assert.Equal(t, lark.DocxBlockTypeBullet, dg1.Descendants[i].BlockType, "dg1 child %d", i)
				}

				// 第二个 details: Text(folded) + Code
				dg2 := result.DescendantGroups[1]
				parent2 := dg2.Descendants[0]
				assert.Equal(t, lark.DocxBlockTypeText, parent2.BlockType)
				require.NotNil(t, parent2.Text)
				require.NotNil(t, parent2.Text.Style)
				assert.True(t, parent2.Text.Style.Folded)
				assert.Contains(t, collectTextContent(parent2.Text.Elements), "配置文件示例")
				assert.Len(t, parent2.Children, 1)
				assert.Equal(t, lark.DocxBlockTypeCode, dg2.Descendants[1].BlockType)
			},
		},
		{
			name: "inline_html",
			file: "../testdata/upload.inline_html.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 11)

				findElement := func(block *lark.DocxBlock, content string) *lark.DocxTextElementTextRun {
					for _, e := range block.Text.Elements {
						if e.TextRun != nil && strings.Contains(e.TextRun.Content, content) {
							return e.TextRun
						}
					}
					return nil
				}
				hasStyle := func(block *lark.DocxBlock, content string, check func(*lark.DocxTextElementStyle) bool) bool {
					tr := findElement(block, content)
					return tr != nil && tr.TextElementStyle != nil && check(tr.TextElementStyle)
				}

				// [0] <u> 和 <ins> → Underline
				assert.True(t, hasStyle(result.TopBlocks[0], "下划线", func(s *lark.DocxTextElementStyle) bool { return s.Underline }))
				assert.True(t, hasStyle(result.TopBlocks[0], "插入文本", func(s *lark.DocxTextElementStyle) bool { return s.Underline }))

				// [1] <b>, <strong>, <i>, <em> → Bold / Italic
				assert.True(t, hasStyle(result.TopBlocks[1], "粗体", func(s *lark.DocxTextElementStyle) bool { return s.Bold }))
				assert.True(t, hasStyle(result.TopBlocks[1], "加粗", func(s *lark.DocxTextElementStyle) bool { return s.Bold }))
				assert.True(t, hasStyle(result.TopBlocks[1], "斜体", func(s *lark.DocxTextElementStyle) bool { return s.Italic }))
				assert.True(t, hasStyle(result.TopBlocks[1], "强调", func(s *lark.DocxTextElementStyle) bool { return s.Italic }))

				// [2] <del>, <s>, <strike> → Strikethrough
				assert.True(t, hasStyle(result.TopBlocks[2], "删除线", func(s *lark.DocxTextElementStyle) bool { return s.Strikethrough }))
				assert.True(t, hasStyle(result.TopBlocks[2], "删除", func(s *lark.DocxTextElementStyle) bool { return s.Strikethrough }))
				assert.True(t, hasStyle(result.TopBlocks[2], "删除效果", func(s *lark.DocxTextElementStyle) bool { return s.Strikethrough }))

				// [3] <code> → InlineCode
				assert.True(t, hasStyle(result.TopBlocks[3], "fmt.Println", func(s *lark.DocxTextElementStyle) bool { return s.InlineCode }))

				// [4] <kbd> → InlineCode
				assert.True(t, hasStyle(result.TopBlocks[4], "Ctrl", func(s *lark.DocxTextElementStyle) bool { return s.InlineCode }))

				// [5] <mark> → BackgroundColor
				assert.True(t, hasStyle(result.TopBlocks[5], "高亮标记", func(s *lark.DocxTextElementStyle) bool {
					return s.BackgroundColor == lark.DocxFontBackgroundColorLightYellow
				}))

				// [6] <a> → Link
				tr := findElement(result.TopBlocks[6], "Claude Code")
				require.NotNil(t, tr)
				require.NotNil(t, tr.TextElementStyle)
				require.NotNil(t, tr.TextElementStyle.Link)
				assert.Contains(t, tr.TextElementStyle.Link.URL, "github.com")

				// [7] <sub>/<sup> → Equation
				hasSubEq := false
				hasSupEq := false
				for _, e := range result.TopBlocks[7].Text.Elements {
					if e.Equation != nil {
						if e.Equation.Content == "_{2}" {
							hasSubEq = true
						}
						if e.Equation.Content == "^{2}" {
							hasSupEq = true
						}
					}
				}
				assert.True(t, hasSubEq, "should have Equation _{2} from <sub>")
				assert.True(t, hasSupEq, "should have Equation ^{2} from <sup>")

				// [8] <br> → 包含换行
				text8 := collectTextContent(result.TopBlocks[8].Text.Elements)
				assert.Contains(t, text8, "\n")

				// [9] 嵌套样式：<b><u>粗体加下划线</u></b>
				assert.True(t, hasStyle(result.TopBlocks[9], "粗体加下划线", func(s *lark.DocxTextElementStyle) bool {
					return s.Bold && s.Underline
				}))
				// <i><mark>斜体高亮</mark></i>
				assert.True(t, hasStyle(result.TopBlocks[9], "斜体高亮", func(s *lark.DocxTextElementStyle) bool {
					return s.Italic && s.BackgroundColor == lark.DocxFontBackgroundColorLightYellow
				}))

				// [10] 混合：Markdown **粗体** + HTML <u>下划线</u>
				assert.True(t, hasStyle(result.TopBlocks[10], "Markdown 粗体", func(s *lark.DocxTextElementStyle) bool { return s.Bold }))
				assert.True(t, hasStyle(result.TopBlocks[10], "HTML 下划线", func(s *lark.DocxTextElementStyle) bool { return s.Underline }))
			},
		},
		{
			name: "html",
			file: "../testdata/upload.html.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Greater(t, len(result.TopBlocks), 0)

				findElement := func(block *lark.DocxBlock, content string) *lark.DocxTextElementTextRun {
					if block == nil || block.Text == nil {
						return nil
					}
					for _, e := range block.Text.Elements {
						if e.TextRun != nil && strings.Contains(e.TextRun.Content, content) {
							return e.TextRun
						}
					}
					return nil
				}
				hasStyle := func(block *lark.DocxBlock, content string, check func(*lark.DocxTextElementStyle) bool) bool {
					tr := findElement(block, content)
					return tr != nil && tr.TextElementStyle != nil && check(tr.TextElementStyle)
				}

				// 第一个块：inline HTML，包含 <kbd> 标签
				block0 := result.TopBlocks[0]
				assert.Equal(t, lark.DocxBlockTypeText, block0.BlockType)
				firstText := collectTextContent(block0.Text.Elements)
				assert.Contains(t, firstText, "按下")
				assert.Contains(t, firstText, "Ctrl")
				assert.True(t, hasStyle(block0, "Ctrl", func(s *lark.DocxTextElementStyle) bool { return s.InlineCode }),
					"should have InlineCode from <kbd>")

				// 第二个块：H<sub>2</sub>O ... mc<sup>2</sup> → Equation
				block1 := result.TopBlocks[1]
				assert.Equal(t, lark.DocxBlockTypeText, block1.BlockType)
				hasSubEq := false
				hasSupEq := false
				for _, e := range block1.Text.Elements {
					if e.Equation != nil {
						if e.Equation.Content == "_{2}" {
							hasSubEq = true
						}
						if e.Equation.Content == "^{2}" {
							hasSupEq = true
						}
					}
				}
				assert.True(t, hasSubEq, "should have Equation _{2} from <sub>")
				assert.True(t, hasSupEq, "should have Equation ^{2} from <sup>")

				// 第三个块：<mark> → BackgroundColor
				block2 := result.TopBlocks[2]
				assert.Equal(t, lark.DocxBlockTypeText, block2.BlockType)
				assert.True(t, hasStyle(block2, "高亮标记", func(s *lark.DocxTextElementStyle) bool {
					return s.BackgroundColor == lark.DocxFontBackgroundColorLightYellow
				}), "should have highlight from <mark>")

				// <p> 中 <strong>larkdown</strong> → Bold，与前后文本在同一 block
				assert.True(t, hasStyle(result.TopBlocks[5], "larkdown", func(s *lark.DocxTextElementStyle) bool { return s.Bold }),
					"should have Bold from <strong> in <p>")
				assert.NotNil(t, findElement(result.TopBlocks[5], "项目的功能矩阵"),
					"normal text should be in same block as bold larkdown")

				// 核心验证：<strong>注意：</strong>... <code>larkdown config</code> 命令。
				// 应在同一个 text block 中，包含 4 个 TextElement，样式正确
				var noticeBlock *lark.DocxBlock
				for _, b := range result.TopBlocks {
					if findElement(b, "注意：") != nil {
						noticeBlock = b
						break
					}
				}
				require.NotNil(t, noticeBlock, "should find notice block")
				require.Equal(t, lark.DocxBlockTypeText, noticeBlock.BlockType)
				require.Len(t, noticeBlock.Text.Elements, 4, "notice block should have 4 elements")
				assert.True(t, hasStyle(noticeBlock, "注意：", func(s *lark.DocxTextElementStyle) bool { return s.Bold }),
					"'注意：' should be bold")
				assert.NotNil(t, findElement(noticeBlock, "配置表单仅作展示用途"),
					"normal text should be in same block")
				assert.True(t, hasStyle(noticeBlock, "larkdown config", func(s *lark.DocxTextElementStyle) bool { return s.InlineCode }),
					"'larkdown config' should be InlineCode")

				// <dd> 中 <code> 标签分组验证
				assert.True(t, hasStyle(result.TopBlocks[8], "tenant_access_token", func(s *lark.DocxTextElementStyle) bool { return s.InlineCode }),
					"should have InlineCode in <dd>")
				assert.NotNil(t, findElement(result.TopBlocks[8], "user_access_token"),
					"both code elements should be in same block")

				// block HTML 部分：应包含表格（DescendantGroup）
				assert.Greater(t, len(result.DescendantGroups), 0, "should have table from HTML block")
			},
		},
		{
			name: "mermaid",
			file: "../testdata/upload.mermaid.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 1)
				assert.Equal(t, lark.DocxBlockTypeAddOns, result.TopBlocks[0].BlockType)
				require.NotNil(t, result.TopBlocks[0].AddOns)
				assert.Equal(t, addOnsMermaidComponentTypeID, result.TopBlocks[0].AddOns.ComponentTypeID)
			},
		},
		{
			name: "plantuml",
			file: "../testdata/upload.plantuml.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 1)
				assert.Equal(t, lark.DocxBlockTypeBoard, result.TopBlocks[0].BlockType)
				require.NotNil(t, result.TopBlocks[0].Board)
				// Board 跟踪信息
				require.Len(t, result.BoardIndices, 1)
				assert.Equal(t, 0, result.BoardIndices[0])
				require.Len(t, result.BoardCodes, 1)
				assert.Contains(t, result.BoardCodes[0], "@startuml")
			},
		},
		{
			name: "span",
			file: "../testdata/upload.span.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 6)

				findElement := func(block *lark.DocxBlock, content string) *lark.DocxTextElementTextRun {
					for _, e := range block.Text.Elements {
						if e.TextRun != nil && strings.Contains(e.TextRun.Content, content) {
							return e.TextRun
						}
					}
					return nil
				}
				hasStyle := func(block *lark.DocxBlock, content string, check func(*lark.DocxTextElementStyle) bool) bool {
					tr := findElement(block, content)
					return tr != nil && tr.TextElementStyle != nil && check(tr.TextElementStyle)
				}

				// [0] color: red → TextColor
				assert.True(t, hasStyle(result.TopBlocks[0], "红色文字", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightPink
				}))

				// [1] background-color: yellow → BackgroundColor
				assert.True(t, hasStyle(result.TopBlocks[1], "黄色背景", func(s *lark.DocxTextElementStyle) bool {
					return s.BackgroundColor == lark.DocxFontBackgroundColorLightYellow
				}))

				// [2] color: blue + background-color: #e6f7ff → Both
				assert.True(t, hasStyle(result.TopBlocks[2], "蓝色文字加浅蓝背景", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightBlue &&
						s.BackgroundColor == lark.DocxFontBackgroundColorLightBlue
				}))

				// [3] color: #ff5722 → TextColor (orange hue)
				assert.True(t, hasStyle(result.TopBlocks[3], "十六进制颜色", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightOrange
				}))

				// [4] nested span: outer color:red + inner background-color:yellow
				assert.True(t, hasStyle(result.TopBlocks[4], "嵌套 span 红字黄底", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightPink &&
						s.BackgroundColor == lark.DocxFontBackgroundColorLightYellow
				}))

				// [5] markdown bold + span color:green
				assert.True(t, hasStyle(result.TopBlocks[5], "Markdown 粗体", func(s *lark.DocxTextElementStyle) bool {
					return s.Bold
				}))
				assert.True(t, hasStyle(result.TopBlocks[5], "绿色 span", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightGreen
				}))
			},
		},
		{
			name: "font",
			file: "../testdata/upload.font.md",
			validate: func(t *testing.T, result *ConvertResult) {
				require.Len(t, result.TopBlocks, 4)

				findElement := func(block *lark.DocxBlock, content string) *lark.DocxTextElementTextRun {
					for _, e := range block.Text.Elements {
						if e.TextRun != nil && strings.Contains(e.TextRun.Content, content) {
							return e.TextRun
						}
					}
					return nil
				}
				hasStyle := func(block *lark.DocxBlock, content string, check func(*lark.DocxTextElementStyle) bool) bool {
					tr := findElement(block, content)
					return tr != nil && tr.TextElementStyle != nil && check(tr.TextElementStyle)
				}

				// [0] <font color="red"> → TextColor
				assert.True(t, hasStyle(result.TopBlocks[0], "红色文字", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightPink
				}))

				// [1] <font color="#0000ff"> → TextColor from hex
				assert.True(t, hasStyle(result.TopBlocks[1], "蓝色十六进制", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightBlue
				}))

				// [2] nested font: inner blue overrides outer red
				assert.True(t, hasStyle(result.TopBlocks[2], "内层蓝色覆盖外层红色", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightBlue
				}))

				// [3] markdown bold + font color:green
				assert.True(t, hasStyle(result.TopBlocks[3], "Markdown 粗体", func(s *lark.DocxTextElementStyle) bool {
					return s.Bold
				}))
				assert.True(t, hasStyle(result.TopBlocks[3], "绿色 font", func(s *lark.DocxTextElementStyle) bool {
					return s.TextColor == lark.DocxFontColorLightGreen
				}))
			},
		},
		{
			name: "no_linkify",
			file: "../testdata/upload.no_linkify.md",
			validate: func(t *testing.T, result *ConvertResult) {
				// 6 个段落块
				require.Len(t, result.TopBlocks, 6)

				findElement := func(block *lark.DocxBlock, content string) *lark.DocxTextElementTextRun {
					for _, e := range block.Text.Elements {
						if e.TextRun != nil && strings.Contains(e.TextRun.Content, content) {
							return e.TextRun
						}
					}
					return nil
				}

				// inline code 不再挂空 Link：飞书 descendant 接口拒绝空 URL（link.url is required）。
				// 容忍飞书对 URL 文本的 auto-linkify 渲染。

				// [0] 普通 inline code
				tr := findElement(result.TopBlocks[0], "行内代码")
				require.NotNil(t, tr, "should find '行内代码'")
				assert.True(t, tr.TextElementStyle.InlineCode, "plain inline code should have InlineCode")
				assert.Nil(t, tr.TextElementStyle.Link, "plain inline code should not carry Link")

				// [1] inline code 包含 URL
				tr = findElement(result.TopBlocks[1], "https://example.com/coderabbit-shared.yaml")
				require.NotNil(t, tr, "should find inline code URL")
				assert.True(t, tr.TextElementStyle.InlineCode, "inline code URL should have InlineCode")
				assert.Nil(t, tr.TextElementStyle.Link, "inline code URL should not carry Link")

				// [2] 裸 URL 自动链接 — 走 AutoLink 路径，应有 Link
				tr = findElement(result.TopBlocks[2], "https://example.com/autolink")
				require.NotNil(t, tr, "should find autolink URL")
				require.NotNil(t, tr.TextElementStyle.Link, "autolink should have Link")
				assert.Equal(t, "https://example.com/autolink", tr.TextElementStyle.Link.URL)

				// [3] 混合段落中的 inline code URL
				tr = findElement(result.TopBlocks[3], "https://example.com/mixed")
				require.NotNil(t, tr, "should find mixed inline code URL")
				assert.True(t, tr.TextElementStyle.InlineCode, "mixed inline code URL should have InlineCode")
				assert.Nil(t, tr.TextElementStyle.Link, "mixed inline code URL should not carry Link")

				// [4] www 裸域名 inline code
				tr = findElement(result.TopBlocks[4], "www.baidu.com")
				require.NotNil(t, tr, "should find www inline code")
				assert.True(t, tr.TextElementStyle.InlineCode, "www inline code should have InlineCode")
				assert.Nil(t, tr.TextElementStyle.Link, "www inline code should not carry Link")

				// [5] raw domain 行包含多个 inline code
				tr = findElement(result.TopBlocks[5], "baidu.com")
				require.NotNil(t, tr, "should find raw domain inline code")
				assert.True(t, tr.TextElementStyle.InlineCode, "raw domain inline code should have InlineCode")
				assert.Nil(t, tr.TextElementStyle.Link, "raw domain inline code should not carry Link")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			_, body, err := ParseFrontMatter(string(data))
			require.NoError(t, err)
			result, err := ConvertMarkdownToDocxBlocks(body, "")
			require.NoError(t, err)
			tc.validate(t, result)
		})
	}
}

// --- Inline HTML 专项测试 ---

func TestInlineHTMLUnderline(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("这是<u>下划线</u>文本", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "下划线" {
			assert.True(t, e.TextRun.TextElementStyle.Underline, "should have underline")
			found = true
		}
	}
	assert.True(t, found, "should find underlined text")
}

func TestInlineHTMLMark(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("这是<mark>高亮</mark>文本", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "高亮" {
			assert.Equal(t, lark.DocxFontBackgroundColorLightYellow, e.TextRun.TextElementStyle.BackgroundColor)
			found = true
		}
	}
	assert.True(t, found, "should find highlighted text")
}

func TestInlineHTMLBold(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("这是<b>粗体</b>文本", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "粗体" {
			assert.True(t, e.TextRun.TextElementStyle.Bold, "should have bold")
			found = true
		}
	}
	assert.True(t, found, "should find bold text")
}

func TestInlineHTMLItalic(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("这是<i>斜体</i>文本", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "斜体" {
			assert.True(t, e.TextRun.TextElementStyle.Italic, "should have italic")
			found = true
		}
	}
	assert.True(t, found, "should find italic text")
}

func TestInlineHTMLStrikethrough(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("这是<del>删除</del>文本", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "删除" {
			assert.True(t, e.TextRun.TextElementStyle.Strikethrough, "should have strikethrough")
			found = true
		}
	}
	assert.True(t, found, "should find strikethrough text")
}

func TestInlineHTMLCode(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("使用<code>fmt.Println</code>函数", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "fmt.Println" {
			assert.True(t, e.TextRun.TextElementStyle.InlineCode, "should have inline code")
			found = true
		}
	}
	assert.True(t, found, "should find inline code text")
}

func TestInlineHTMLKbd(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("按下<kbd>Ctrl</kbd>键", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "Ctrl" {
			assert.True(t, e.TextRun.TextElementStyle.InlineCode, "should have inline code for kbd")
			found = true
		}
	}
	assert.True(t, found, "should find kbd text")
}

func TestInlineHTMLBr(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("第一行<br>第二行", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	text := collectTextContent(result.TopBlocks[0].Text.Elements)
	assert.Contains(t, text, "第一行")
	assert.Contains(t, text, "\n")
	assert.Contains(t, text, "第二行")
}

func TestInlineHTMLBrSelfClosing(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("第一行<br/>第二行", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	text := collectTextContent(result.TopBlocks[0].Text.Elements)
	assert.Contains(t, text, "\n")
}

func TestInlineHTMLLink(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`点击<a href="https://example.com">这里</a>`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "这里" {
			require.NotNil(t, e.TextRun.TextElementStyle.Link)
			assert.Equal(t, "https://example.com", e.TextRun.TextElementStyle.Link.URL)
			found = true
		}
	}
	assert.True(t, found, "should find link text")
}

func TestInlineHTMLNestedStyles(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("这是<b><u>粗体下划线</u></b>文本", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	found := false
	for _, e := range elements {
		if e.TextRun != nil && e.TextRun.Content == "粗体下划线" {
			assert.True(t, e.TextRun.TextElementStyle.Bold, "should have bold")
			assert.True(t, e.TextRun.TextElementStyle.Underline, "should have underline")
			found = true
		}
	}
	assert.True(t, found, "should find nested styled text")
}

func TestInlineHTMLMixedWithMarkdown(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("**粗体** 和 <u>下划线</u> 混合", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elements := result.TopBlocks[0].Text.Elements
	hasBold := false
	hasUnderline := false
	for _, e := range elements {
		if e.TextRun != nil {
			if e.TextRun.TextElementStyle != nil && e.TextRun.TextElementStyle.Bold {
				hasBold = true
			}
			if e.TextRun.TextElementStyle != nil && e.TextRun.TextElementStyle.Underline {
				hasUnderline = true
			}
		}
	}
	assert.True(t, hasBold, "should have bold from markdown")
	assert.True(t, hasUnderline, "should have underline from HTML")
}

func TestInlineHTMLSubSupAsEquation(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks("H<sub>2</sub>O", "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elems := result.TopBlocks[0].Text.Elements
	// H → TextRun, _{2} → Equation, O → TextRun
	require.Len(t, elems, 3)
	assert.Equal(t, "H", elems[0].TextRun.Content)
	assert.Equal(t, "_{2}", elems[1].Equation.Content)
	assert.Equal(t, "O", elems[2].TextRun.Content)
}

// --- Block-level HTML 专项测试 ---

func TestBlockHTMLTable(t *testing.T) {
	md := `<table>
<tr><th>Name</th><th>Age</th></tr>
<tr><td>Alice</td><td>30</td></tr>
</table>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Greater(t, len(result.DescendantGroups), 0)

	// 找到 table descendant
	found := false
	for _, dg := range result.DescendantGroups {
		for _, d := range dg.Descendants {
			if d.BlockType == lark.DocxBlockTypeTable {
				assert.Equal(t, int64(2), d.Table.Property.RowSize)
				assert.Equal(t, int64(2), d.Table.Property.ColumnSize)
				found = true
			}
		}
	}
	assert.True(t, found, "should have table block")
}

func TestBlockHTMLHeading(t *testing.T) {
	md := `<h2>HTML 标题</h2>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	found := false
	for _, b := range result.TopBlocks {
		if b.BlockType == lark.DocxBlockTypeHeading2 && b.Heading2 != nil {
			text := collectTextContent(b.Heading2.Elements)
			if strings.Contains(text, "HTML 标题") {
				found = true
			}
		}
	}
	assert.True(t, found, "should have h2 block")
}

func TestBlockHTMLList(t *testing.T) {
	md := `<ul>
<li>item1</li>
<li>item2</li>
</ul>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	bulletCount := 0
	for _, b := range result.TopBlocks {
		if b.BlockType == lark.DocxBlockTypeBullet {
			bulletCount++
		}
	}
	assert.Equal(t, 2, bulletCount, "should have 2 bullet items")
}

func TestBlockHTMLDivider(t *testing.T) {
	md := `<hr>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	found := false
	for _, b := range result.TopBlocks {
		if b.BlockType == lark.DocxBlockTypeDivider {
			found = true
		}
	}
	assert.True(t, found, "should have divider block")
}

func TestBlockHTMLCodeBlock(t *testing.T) {
	md := "<pre><code class=\"language-go\">fmt.Println(\"hello\")\n</code></pre>"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	found := false
	for _, b := range result.TopBlocks {
		if b.BlockType == lark.DocxBlockTypeCode && b.Code != nil {
			assert.Equal(t, lark.DocxCodeLanguageGo, b.Code.Style.Language)
			assert.Contains(t, b.Code.Elements[0].TextRun.Content, "fmt.Println")
			found = true
		}
	}
	assert.True(t, found, "should have code block")
}

// --- Bug fix 回归测试 ---

func TestHTMLTableRowspanColspan(t *testing.T) {
	md := `<table>
<tr><th>A</th><th>B</th><th>C</th><th>D</th></tr>
<tr><td rowspan="2">R</td><td>1</td><td>2</td><td>3</td></tr>
<tr><td>4</td><td>5</td><td>6</td></tr>
<tr><td colspan="2">Wide</td><td>7</td><td>8</td></tr>
</table>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Greater(t, len(result.DescendantGroups), 0)

	dg := &result.DescendantGroups[0]

	// 查找 table block
	var tableBlock *lark.DocxBlock
	for _, d := range dg.Descendants {
		if d.BlockType == lark.DocxBlockTypeTable {
			tableBlock = d
			break
		}
	}
	require.NotNil(t, tableBlock, "should have table block")
	assert.Equal(t, int64(4), tableBlock.Table.Property.RowSize)
	assert.Equal(t, int64(4), tableBlock.Table.Property.ColumnSize)
	// 4 rows × 4 cols = 16 cells
	assert.Equal(t, 16, len(tableBlock.Table.Cells))

	// 验证 MergeRegions
	require.Len(t, dg.MergeRegions, 2, "should have 2 merge regions (rowspan=2 + colspan=2)")
	// rowspan=2: row 1-3, col 0-1
	assert.Equal(t, MergeRegion{RowStartIndex: 1, RowEndIndex: 3, ColumnStartIndex: 0, ColumnEndIndex: 1}, dg.MergeRegions[0])
	// colspan=2: row 3-4, col 0-2
	assert.Equal(t, MergeRegion{RowStartIndex: 3, RowEndIndex: 4, ColumnStartIndex: 0, ColumnEndIndex: 2}, dg.MergeRegions[1])
}

func TestHTMLTableRowspanContentDuplication(t *testing.T) {
	md := `<table>
<tr><td rowspan="2">Merged</td><td>A</td></tr>
<tr><td>B</td></tr>
</table>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 收集所有 cell 内容（按顺序）
	var cellTexts []string
	for _, dg := range result.DescendantGroups {
		for _, d := range dg.Descendants {
			if d.BlockType == lark.DocxBlockTypeText && d.Text != nil {
				cellTexts = append(cellTexts, collectTextContent(d.Text.Elements))
			}
		}
	}
	// grid: row0=[Merged, A], row1=[空, B] — 被覆盖的单元格内容为空
	require.Len(t, cellTexts, 4)
	assert.Equal(t, "Merged", cellTexts[0])
	assert.Equal(t, "A", cellTexts[1])
	assert.Equal(t, " ", cellTexts[2]) // rowspan 被覆盖的单元格为空（emptyTextElement 用空格占位）
	assert.Equal(t, "B", cellTexts[3])
}

func TestEmptyCellContentNotOmitted(t *testing.T) {
	md := "| a | |\n| --- | --- |\n| | b |"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.DescendantGroups, 1)

	// 检查所有 text content block 的 JSON 都包含 "content"
	for _, d := range result.DescendantGroups[0].Descendants {
		if d.BlockType == lark.DocxBlockTypeText && d.Text != nil {
			for _, e := range d.Text.Elements {
				if e.TextRun != nil {
					data, err := json.Marshal(e.TextRun)
					require.NoError(t, err)
					assert.Contains(t, string(data), `"content"`,
						"content field should be present in JSON even for empty cells")
				}
			}
		}
	}
}

func TestHTMLListWithNestedTable(t *testing.T) {
	md := `<ul>
<li>纯文本项</li>
<li>包含表格
<table><tr><td>K</td><td>V</td></tr></table>
</li>
<li>第三项</li>
</ul>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 应该有 3 个 bullet + 至少 1 个 table DescendantGroup
	bulletCount := 0
	for _, b := range result.TopBlocks {
		if b != nil && b.BlockType == lark.DocxBlockTypeBullet {
			bulletCount++
		}
	}
	assert.Equal(t, 3, bulletCount, "should have 3 bullet items")
	assert.Greater(t, len(result.DescendantGroups), 0, "should have table descendant group")

	// table 应该在 bullets 之间
	tableFound := false
	for _, dg := range result.DescendantGroups {
		for _, d := range dg.Descendants {
			if d.BlockType == lark.DocxBlockTypeTable {
				tableFound = true
			}
		}
	}
	assert.True(t, tableFound, "should have table from nested <table> in <li>")
}

func TestHTMLListEmptyItem(t *testing.T) {
	md := `<ul>
<li></li>
<li>text</li>
</ul>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	bulletCount := 0
	for _, b := range result.TopBlocks {
		if b.BlockType == lark.DocxBlockTypeBullet {
			bulletCount++
		}
	}
	assert.Equal(t, 2, bulletCount, "should have 2 bullet items including empty one")

	// 空项应有 " " 而非 ""
	first := result.TopBlocks[0]
	require.NotNil(t, first.Bullet)
	require.Len(t, first.Bullet.Elements, 1)
	assert.Equal(t, " ", first.Bullet.Elements[0].TextRun.Content)
}

// --- Inline HTML <span>/<font> 专项测试 ---

func TestInlineHTMLSpanColor(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`文本 <span style="color: red">红色</span> 结束`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	found := false
	for _, e := range result.TopBlocks[0].Text.Elements {
		if e.TextRun != nil && e.TextRun.Content == "红色" {
			assert.Equal(t, lark.DocxFontColorLightPink, e.TextRun.TextElementStyle.TextColor)
			found = true
		}
	}
	assert.True(t, found, "should find red-colored text")
}

func TestInlineHTMLSpanBgColor(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`文本 <span style="background-color: yellow">高亮</span> 结束`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	found := false
	for _, e := range result.TopBlocks[0].Text.Elements {
		if e.TextRun != nil && e.TextRun.Content == "高亮" {
			assert.Equal(t, lark.DocxFontBackgroundColorLightYellow, e.TextRun.TextElementStyle.BackgroundColor)
			found = true
		}
	}
	assert.True(t, found, "should find highlighted text")
}

func TestInlineHTMLSpanBothColors(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`<span style="color: blue; background-color: yellow">双色</span>`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elem := result.TopBlocks[0].Text.Elements[0]
	require.NotNil(t, elem.TextRun)
	assert.Equal(t, "双色", elem.TextRun.Content)
	assert.Equal(t, lark.DocxFontColorLightBlue, elem.TextRun.TextElementStyle.TextColor)
	assert.Equal(t, lark.DocxFontBackgroundColorLightYellow, elem.TextRun.TextElementStyle.BackgroundColor)
}

func TestInlineHTMLFontColor(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`文本 <font color="blue">蓝色</font> 结束`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	found := false
	for _, e := range result.TopBlocks[0].Text.Elements {
		if e.TextRun != nil && e.TextRun.Content == "蓝色" {
			assert.Equal(t, lark.DocxFontColorLightBlue, e.TextRun.TextElementStyle.TextColor)
			found = true
		}
	}
	assert.True(t, found, "should find blue-colored text")
}

func TestInlineHTMLFontColorHex(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`<font color="#0000ff">蓝色hex</font>`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	elem := result.TopBlocks[0].Text.Elements[0]
	require.NotNil(t, elem.TextRun)
	assert.Equal(t, "蓝色hex", elem.TextRun.Content)
	assert.Equal(t, lark.DocxFontColorLightBlue, elem.TextRun.TextElementStyle.TextColor)
}

func TestInlineHTMLSpanNestedColors(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`<span style="color: red"><span style="background-color: yellow">红字黄底</span></span>`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	found := false
	for _, e := range result.TopBlocks[0].Text.Elements {
		if e.TextRun != nil && e.TextRun.Content == "红字黄底" {
			assert.Equal(t, lark.DocxFontColorLightPink, e.TextRun.TextElementStyle.TextColor)
			assert.Equal(t, lark.DocxFontBackgroundColorLightYellow, e.TextRun.TextElementStyle.BackgroundColor)
			found = true
		}
	}
	assert.True(t, found, "should find nested styled text")
}

func TestInlineHTMLFontNestedOverride(t *testing.T) {
	result, err := ConvertMarkdownToDocxBlocks(`<font color="red"><font color="blue">内层蓝</font></font>`, "")
	require.NoError(t, err)
	require.Len(t, result.TopBlocks, 1)

	found := false
	for _, e := range result.TopBlocks[0].Text.Elements {
		if e.TextRun != nil && e.TextRun.Content == "内层蓝" {
			assert.Equal(t, lark.DocxFontColorLightBlue, e.TextRun.TextElementStyle.TextColor)
			found = true
		}
	}
	assert.True(t, found, "inner font color should override outer")
}

func TestHTMLTableMergeRegions(t *testing.T) {
	md := `<table>
<tr><td rowspan="3">A</td><td>B</td><td>C</td></tr>
<tr><td colspan="2">Wide</td></tr>
<tr><td>E</td><td>F</td></tr>
</table>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.DescendantGroups, 1)

	dg := &result.DescendantGroups[0]
	require.Len(t, dg.MergeRegions, 2)

	// rowspan=3: rows 0-3, col 0-1
	assert.Equal(t, MergeRegion{RowStartIndex: 0, RowEndIndex: 3, ColumnStartIndex: 0, ColumnEndIndex: 1}, dg.MergeRegions[0])
	// colspan=2: row 1-2, cols 1-3
	assert.Equal(t, MergeRegion{RowStartIndex: 1, RowEndIndex: 2, ColumnStartIndex: 1, ColumnEndIndex: 3}, dg.MergeRegions[1])
}

func TestHTMLTableNoMergeRegionsForSimpleTable(t *testing.T) {
	md := `<table>
<tr><td>A</td><td>B</td></tr>
<tr><td>C</td><td>D</td></tr>
</table>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.DescendantGroups, 1)
	assert.Nil(t, result.DescendantGroups[0].MergeRegions, "simple table should have no merge regions")
}

// --- <img> 图片上传支持测试 ---

func TestInlineHTMLImgAsBlock(t *testing.T) {
	md := `<div><p>text <img src="x.png"> more</p></div>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 应产生 text + image + text 三个 TopBlocks
	require.Len(t, result.TopBlocks, 3)
	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
	assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[1].BlockType)
	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[2].BlockType)

	// 验证 ImageIndices/ImagePaths
	require.Len(t, result.ImageIndices, 1)
	assert.Equal(t, 1, result.ImageIndices[0])
	require.Len(t, result.ImagePaths, 1)
	assert.Equal(t, "x.png", result.ImagePaths[0])
}

func TestHTMLTableWithImage(t *testing.T) {
	md := `<table>
<tr><td><img src="x.png"></td><td>text</td></tr>
</table>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.DescendantGroups, 1)

	dg := result.DescendantGroups[0]

	// 应包含 Image block 在 descendants 中
	hasImage := false
	for _, d := range dg.Descendants {
		if d.BlockType == lark.DocxBlockTypeImage {
			hasImage = true
		}
	}
	assert.True(t, hasImage, "should have Image block in descendants")

	// DescendantImages 应记录路径
	require.Len(t, dg.DescendantImages, 1)
	assert.Equal(t, "x.png", dg.DescendantImages[0].ImagePath)
	assert.NotEmpty(t, dg.DescendantImages[0].TempBlockID)
}

func TestMarkdownInlineHTMLImg(t *testing.T) {
	md := `正常文字 <img src="x.png"> 后续文字`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 应产生 text + image + text
	require.Len(t, result.TopBlocks, 3)
	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
	assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[1].BlockType)
	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[2].BlockType)

	require.Len(t, result.ImagePaths, 1)
	assert.Equal(t, "x.png", result.ImagePaths[0])
}

func TestMarkdownInlineImageSplitsParagraph(t *testing.T) {
	md := "前文 ![alt](img.png) 后文"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	// 应产生 text + image + text
	require.Len(t, result.TopBlocks, 3)
	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
	assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[1].BlockType)
	assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[2].BlockType)

	require.Len(t, result.ImagePaths, 1)
	assert.Equal(t, "img.png", result.ImagePaths[0])
}

func TestMarkdownSingleImageParagraph(t *testing.T) {
	// 原有场景：段落中只有一个图片，应继续正常工作
	md := "![alt](image.png)"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.TopBlocks, 1)
	assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[0].BlockType)
	require.Len(t, result.ImageIndices, 1)
	assert.Equal(t, 0, result.ImageIndices[0])
}

func TestHTMLBlockImgStandalone(t *testing.T) {
	md := `<div><img src="photo.jpg"></div>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)

	require.Len(t, result.TopBlocks, 1)
	assert.Equal(t, lark.DocxBlockTypeImage, result.TopBlocks[0].BlockType)
	require.Len(t, result.ImagePaths, 1)
	assert.Equal(t, "photo.jpg", result.ImagePaths[0])
}

func TestConvertFileLink(t *testing.T) {
	// 创建临时目录和临时文件
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "report.pdf")
	require.NoError(t, os.WriteFile(tmpFile, []byte("dummy pdf"), 0o644))

	t.Run("本地文件链接生成 File block", func(t *testing.T) {
		md := "[测试报告](./report.pdf)"
		result, err := ConvertMarkdownToDocxBlocks(md, tmpDir)
		require.NoError(t, err)

		// 应产生一个 File block
		require.Len(t, result.TopBlocks, 1)
		assert.Equal(t, lark.DocxBlockTypeFile, result.TopBlocks[0].BlockType)
		require.NotNil(t, result.TopBlocks[0].File)
		assert.Equal(t, "测试报告", result.TopBlocks[0].File.Name)

		// FileIndices/FilePaths 应正确记录
		require.Len(t, result.FileIndices, 1)
		assert.Equal(t, 0, result.FileIndices[0])
		require.Len(t, result.FilePaths, 1)
		assert.Equal(t, "./report.pdf", result.FilePaths[0])
	})

	t.Run("HTTP 链接保持普通链接", func(t *testing.T) {
		md := "[Google](https://google.com)"
		result, err := ConvertMarkdownToDocxBlocks(md, tmpDir)
		require.NoError(t, err)

		require.Len(t, result.TopBlocks, 1)
		assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
		assert.Empty(t, result.FileIndices)
	})

	t.Run("文件不存在保持普通链接", func(t *testing.T) {
		md := "[不存在](./nonexistent.pdf)"
		result, err := ConvertMarkdownToDocxBlocks(md, tmpDir)
		require.NoError(t, err)

		require.Len(t, result.TopBlocks, 1)
		assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
		assert.Empty(t, result.FileIndices)
	})

	t.Run("mdDir 为空时保持普通链接", func(t *testing.T) {
		md := "[测试报告](./report.pdf)"
		result, err := ConvertMarkdownToDocxBlocks(md, "")
		require.NoError(t, err)

		require.Len(t, result.TopBlocks, 1)
		assert.Equal(t, lark.DocxBlockTypeText, result.TopBlocks[0].BlockType)
		assert.Empty(t, result.FileIndices)
	})

	t.Run("链接文本为空时使用文件名", func(t *testing.T) {
		// goldmark 将 [](./report.pdf) 解析为空文本链接
		md := "[](./report.pdf)"
		result, err := ConvertMarkdownToDocxBlocks(md, tmpDir)
		require.NoError(t, err)

		require.Len(t, result.TopBlocks, 1)
		assert.Equal(t, lark.DocxBlockTypeFile, result.TopBlocks[0].BlockType)
		assert.Equal(t, "report.pdf", result.TopBlocks[0].File.Name)
	})

	t.Run("段落中混合文本和文件链接", func(t *testing.T) {
		md := "请查看 [测试报告](./report.pdf) 了解详情"
		result, err := ConvertMarkdownToDocxBlocks(md, tmpDir)
		require.NoError(t, err)

		// 应产生 2 个块：text("请查看 ") + file
		// 注意：flushText 会在遇到文件链接时 flush 已收集的 text
		require.GreaterOrEqual(t, len(result.TopBlocks), 2)
		// 最后一个块之前应有 text，然后有 file block
		foundFile := false
		for _, block := range result.TopBlocks {
			if block.BlockType == lark.DocxBlockTypeFile {
				foundFile = true
				assert.Equal(t, "测试报告", block.File.Name)
			}
		}
		assert.True(t, foundFile, "应包含 File block")
		require.Len(t, result.FileIndices, 1)
	})
}

func TestHTMLTableMultipleImagesInCell(t *testing.T) {
	md := `<table>
<tr><td>text <img src="a.png"> mid <img src="b.png"> end</td></tr>
</table>`
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	require.NoError(t, err)
	require.Len(t, result.DescendantGroups, 1)

	dg := result.DescendantGroups[0]
	require.Len(t, dg.DescendantImages, 2)
	assert.Equal(t, "a.png", dg.DescendantImages[0].ImagePath)
	assert.Equal(t, "b.png", dg.DescendantImages[1].ImagePath)
}

// TestForkPatchSerialization 锁住 amzyang/lark fork commit bd0f8c9 的 3 处序列化行为：
// 1. int64 index 字段去 omitempty（BatchDelete / 表格合并），index=0 必须保留。
// 2. DocxBlock.Divider 改指针类型，非 nil 时必须能序列化为对象。
// 3. DocxBlock.AddOns 加 omitempty，nil 时不应出现。
func TestForkPatchSerialization(t *testing.T) {
	t.Run("BatchDeleteStartIndexZero", func(t *testing.T) {
		req := &lark.BatchDeleteDocxBlockReq{StartIndex: 0, EndIndex: 1}
		data, err := json.Marshal(req)
		require.NoError(t, err)
		s := string(data)
		assert.Contains(t, s, `"start_index":0`, "start_index=0 必须保留")
		assert.Contains(t, s, `"end_index":1`)
	})

	t.Run("MergeTableCellsZeroIndices", func(t *testing.T) {
		req := &lark.UpdateDocxBlockReqMergeTableCells{
			RowStartIndex:    0,
			RowEndIndex:      1,
			ColumnStartIndex: 0,
			ColumnEndIndex:   1,
		}
		data, err := json.Marshal(req)
		require.NoError(t, err)
		s := string(data)
		assert.Contains(t, s, `"row_start_index":0`)
		assert.Contains(t, s, `"column_start_index":0`)
	})

	t.Run("DividerPointerSerializes", func(t *testing.T) {
		block := &lark.DocxBlock{
			BlockType: lark.DocxBlockTypeDivider,
			Divider:   &lark.DocxBlockDivider{},
		}
		data, err := json.Marshal(block)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"divider":{}`,
			"Divider 改指针类型后必须能序列化为对象")
	})

	t.Run("AddOnsOmitEmptyWhenNil", func(t *testing.T) {
		block := &lark.DocxBlock{BlockType: lark.DocxBlockTypeText}
		data, err := json.Marshal(block)
		require.NoError(t, err)
		assert.NotContains(t, string(data), `"add_ons"`,
			"AddOns 加 omitempty 后 nil 时不应出现在 JSON")
	})
}
