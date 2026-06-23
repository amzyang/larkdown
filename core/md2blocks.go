package core

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chyroc/lark"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// ConvertResult 是本地 Markdown → DocxBlock 转换的输出
type ConvertResult struct {
	TopBlocks        []*lark.DocxBlock // 一级块（可直接 CreateDocxBlocks 插入）
	DescendantGroups []DescendantGroup // 需要 descendant API 的嵌套结构
	ImageIndices     []int             // TopBlocks 中 Image block 的位置索引
	ImagePaths       []string          // 对应 ImageIndices 的图片路径
	BoardIndices     []int             // TopBlocks 中 Board block 的位置索引
	BoardCodes       []string          // 对应 BoardIndices 的 PlantUML 源码
	FileIndices      []int             // TopBlocks 中 File block 的位置索引
	FilePaths        []string          // 对应 FileIndices 的本地文件路径

	// MentionUserNames 记录 @人 mention 的 user-id → 显示名，
	// 用于写入失败时把 mention 降级为纯文本（见 uploader 的 mention fallback）。
	MentionUserNames map[string]string

	dgMap map[int]*DescendantGroup // lazy-init: TopBlockIndex → DescendantGroup
}

// descendantGroupAt 返回索引 idx 处的 DescendantGroup（O(1) 查找）
func (r *ConvertResult) descendantGroupAt(idx int) *DescendantGroup {
	if r.dgMap == nil {
		r.dgMap = make(map[int]*DescendantGroup, len(r.DescendantGroups))
		for i := range r.DescendantGroups {
			r.dgMap[r.DescendantGroups[i].TopBlockIndex] = &r.DescendantGroups[i]
		}
	}
	return r.dgMap[idx]
}

// MergeRegion 表示表格中需要合并的单元格区域（左闭右开）
type MergeRegion struct {
	RowStartIndex    int64
	RowEndIndex      int64
	ColumnStartIndex int64
	ColumnEndIndex   int64
}

// DescendantGroup 表示一个需要 descendant API 插入的嵌套结构
type DescendantGroup struct {
	TopBlockIndex    int               // 在 TopBlocks 中的占位位置
	ChildrenIDs      []string          // 顶层临时 block_id 列表
	Descendants      []*lark.DocxBlock // 所有后代块（扁平数组，含父子 children 引用）
	MergeRegions     []MergeRegion     // 表格合并区域（仅 Table 使用）
	DescendantImages []DescendantImage // 后代中的图片（表格单元格等）
}

// DescendantImage 记录 descendant 中的图片信息
type DescendantImage struct {
	TempBlockID string // 转换阶段分配的临时 block ID
	ImagePath   string
}

// detailsCapture 记录 <details> capture 的快照位置
type detailsCapture struct {
	summary       string
	startTopIdx   int
	startDGIdx    int
	startImgIdx   int
	startFileIdx  int
	startBoardIdx int
}

// converter 持有转换状态
type converter struct {
	source       []byte
	result       *ConvertResult
	idGen        *blockIDGenerator
	mdDir        string            // Markdown 文件所在目录（用于解析本地文件链接）
	detailsStack []*detailsCapture // 支持嵌套 <details>
	cite         *citePending      // 当前正在解析的 <cite> 标签（行内引用）
}

type blockIDGenerator struct {
	counter int
}

func (g *blockIDGenerator) next(prefix string) string {
	g.counter++
	return fmt.Sprintf("%s_%d", prefix, g.counter)
}

func newMarkdownParser() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			&MathExtension{},
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)
}

// ConvertMarkdownToDocxBlocks 将 Markdown 转换为飞书 DocxBlock
// mdDir 是 Markdown 文件所在目录，用于解析本地文件链接（如 [report](./report.pdf)）
func ConvertMarkdownToDocxBlocks(markdown, mdDir string) (*ConvertResult, error) {
	source := []byte(markdown)
	md := newMarkdownParser()
	reader := text.NewReader(source)
	doc := md.Parser().Parse(reader)

	c := &converter{
		source: source,
		result: &ConvertResult{},
		idGen:  &blockIDGenerator{},
		mdDir:  mdDir,
	}

	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		c.convertBlock(child)
	}

	return c.result, nil
}

// convertBlock 将 block-level AST 节点转为 DocxBlock
func (c *converter) convertBlock(node ast.Node) {
	switch n := node.(type) {
	case *ast.Paragraph:
		c.convertParagraph(n)
	case *ast.Heading:
		c.convertHeading(n)
	case *ast.List:
		c.convertList(n)
	case *ast.FencedCodeBlock:
		c.convertFencedCodeBlock(n)
	case *ast.CodeBlock:
		c.convertCodeBlock(n)
	case *ast.ThematicBreak:
		c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeDivider,
			Divider:   &lark.DocxBlockDivider{},
		})
	case *ast.Blockquote:
		c.convertBlockquote(n)
	case *MathBlock:
		c.convertMathBlock(n)
	case *ast.HTMLBlock:
		c.convertHTMLBlock(n)
	case *east.Table:
		c.convertTable(n)
	default:
		// 未知块类型，尝试处理其 children
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			c.convertBlock(child)
		}
	}
}

func (c *converter) addTopBlock(block *lark.DocxBlock) int {
	idx := len(c.result.TopBlocks)
	c.result.TopBlocks = append(c.result.TopBlocks, block)
	return idx
}

// --- Paragraph ---

func (c *converter) convertParagraph(n *ast.Paragraph) {
	c.convertParagraphContent(n)
}

// convertParagraphContent 遍历段落子节点，遇到图片时 flush 文本并插入独立 image block
func (c *converter) convertParagraphContent(parent ast.Node) {
	var elements []*lark.DocxTextElement
	var htmlStyleStack []htmlStyleEntry

	flushText := func() {
		if len(elements) == 0 {
			return
		}
		c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeText,
			Text:      &lark.DocxBlockText{Elements: elements},
		})
		elements = nil
	}

	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		// 行内 <cite> 引用（@文档/@人）
		if c.handleCiteInline(child, &elements) {
			continue
		}
		// Markdown inline image → 拆分
		if img, ok := child.(*ast.Image); ok {
			flushText()
			c.convertImage(img)
			continue
		}
		// Markdown link → 本地文件检测
		if link, ok := child.(*ast.Link); ok {
			dest := string(link.Destination)
			if c.isLocalFile(dest) {
				flushText()
				name := extractLinkText(link, c.source)
				if name == "" {
					name = filepath.Base(dest)
				}
				idx := c.addTopBlock(&lark.DocxBlock{
					BlockType: lark.DocxBlockTypeFile,
					File:      &lark.DocxBlockFile{Token: "", Name: name},
				})
				c.result.FileIndices = append(c.result.FileIndices, idx)
				c.result.FilePaths = append(c.result.FilePaths, dest)
				continue
			}
		}
		// HTML <img> 标签 → 拆分
		if raw, ok := child.(*ast.RawHTML); ok {
			tag := string(raw.Segments.Value(c.source))
			tagName, _, attrs := parseHTMLTag(tag)
			if tagName == "img" {
				if src, ok := attrs["src"]; ok && src != "" {
					flushText()
					idx := c.addTopBlock(&lark.DocxBlock{
						BlockType: lark.DocxBlockTypeImage,
						Image:     &lark.DocxBlockImage{Token: ""},
					})
					c.result.ImageIndices = append(c.result.ImageIndices, idx)
					c.result.ImagePaths = append(c.result.ImagePaths, src)
				}
				continue
			}
			c.handleInlineHTMLTag(tag, &htmlStyleStack, &elements)
			continue
		}
		if tag := findSubSup(htmlStyleStack); tag != "" {
			text := extractInlineText(child, c.source)
			if text != "" {
				prefix := "_"
				if tag == "sup" {
					prefix = "^"
				}
				elements = append(elements, &lark.DocxTextElement{
					Equation: &lark.DocxTextElementEquation{
						Content: prefix + "{" + text + "}",
					},
				})
			}
			continue
		}
		baseStyle := mergeHTMLStyles(htmlStyleStack)
		c.walkInline(child, baseStyle, &elements)
	}
	c.flushCite(&elements) // 兜底：未闭合的 <cite>
	flushText()
}

// --- Heading ---

func (c *converter) convertHeading(n *ast.Heading) {
	level := n.Level
	if level < 1 {
		level = 1
	}
	if level > 9 {
		level = 9
	}

	elements := c.collectInlineElements(n)
	blockText := &lark.DocxBlockText{Elements: elements}

	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockType(2 + level), // Heading1=3, Heading2=4, ...
	}
	setHeadingField(block, level, blockText)
	c.addTopBlock(block)
}

func setHeadingField(block *lark.DocxBlock, level int, text *lark.DocxBlockText) {
	switch level {
	case 1:
		block.Heading1 = text
	case 2:
		block.Heading2 = text
	case 3:
		block.Heading3 = text
	case 4:
		block.Heading4 = text
	case 5:
		block.Heading5 = text
	case 6:
		block.Heading6 = text
	case 7:
		block.Heading7 = text
	case 8:
		block.Heading8 = text
	case 9:
		block.Heading9 = text
	}
}

// --- List ---

func (c *converter) convertList(n *ast.List) {
	// 列表项若含「需要作为 child 上传的块子节点」（子列表 / 代码块），
	// 走 nested 路径生成 DescendantGroup；否则走扁平快路径。
	needsDescendant := false
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		if listItem, ok := item.(*ast.ListItem); ok && listItemHasDescendantChild(listItem) {
			needsDescendant = true
			break
		}
	}

	if needsDescendant {
		c.convertNestedList(n)
		return
	}

	// 扁平列表，直接输出一级块
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		c.convertFlatListItem(n, listItem)
	}
}

// listItemHasDescendantChild 判断列表项是否含需要作为 child 上传的块子节点：
// 首个纯文本段落作为列表项本体，除此之外的任何内容（含图段落、第二段及以后的段落、
// 子列表、代码块、分隔线、数学块、表格、引用等）都需走 DescendantGroup 路径，
// 否则会被丢弃。
func listItemHasDescendantChild(item *ast.ListItem) bool {
	first := true
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		switch ch := child.(type) {
		case *ast.Paragraph:
			if paragraphHasImage(ch) || !first {
				return true
			}
		case *ast.TextBlock:
			if nodeHasImage(ch) || !first {
				return true
			}
		default:
			return true
		}
		first = false
	}
	return false
}

func (c *converter) convertFlatListItem(list *ast.List, item *ast.ListItem) {
	// 检查是否为 todo
	isTodo, done := c.checkTodo(item)

	// 收集 inline 内容（从第一个 paragraph child）
	var elements []*lark.DocxTextElement
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if p, ok := child.(*ast.Paragraph); ok {
			elements = c.collectInlineElements(p)
			break
		}
		if tp, ok := child.(*ast.TextBlock); ok {
			elements = c.collectInlineElements(tp)
			break
		}
	}
	if len(elements) == 0 {
		elements = []*lark.DocxTextElement{emptyTextElement()}
	}

	blockText := &lark.DocxBlockText{Elements: elements}

	if isTodo {
		blockText.Style = &lark.DocxTextStyle{Done: done}
		c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeTodo,
			Todo:      blockText,
		})
		return
	}

	if list.IsOrdered() {
		c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeOrdered,
			Ordered:   blockText,
		})
	} else {
		c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeBullet,
			Bullet:    blockText,
		})
	}
}

func (c *converter) convertNestedList(list *ast.List) {
	// 每个顶层 list item 占独立槽位，与飞书「N 个顶层块」的结构对齐
	// （飞书把列表顶层项作为文档的直接子块返回）。这样 round-trip 时
	// 本地槽位数与远程顶层块数一致，未变更的列表不会被整体删除重建。
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		ids, descs, imgs := c.buildListItemDescendants(list, listItem)
		// 无嵌套子项（descs 只含 item 自身）→ 普通顶层块（text-like 签名，
		// 与远程无 children 的列表块对齐）。
		if len(descs) == 1 {
			c.addTopBlock(descs[0])
			continue
		}
		// 有嵌套子项 → 单根 descendant group（serializeBlockTree 与远程对称）。
		idx := c.addTopBlock(nil)
		c.result.DescendantGroups = append(c.result.DescendantGroups, DescendantGroup{
			TopBlockIndex:    idx,
			ChildrenIDs:      ids,
			Descendants:      descs,
			DescendantImages: imgs,
		})
	}
}

func (c *converter) buildListItemDescendants(list *ast.List, item *ast.ListItem) ([]string, []*lark.DocxBlock, []DescendantImage) {
	isTodo, done := c.checkTodo(item)
	var elements []*lark.DocxTextElement
	var childIDs []string
	var descendants []*lark.DocxBlock
	var descImages []DescendantImage

	// 首个纯文本段落作为列表项本体；其余子块（含图段落、第二段及以后的段落、
	// 代码块、分隔线、数学块、子列表等）作为该列表项的 child。
	bodySeen := false
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if !bodySeen {
			if p, ok := child.(*ast.Paragraph); ok && !paragraphHasImage(p) {
				elements = c.collectInlineElements(p)
				bodySeen = true
				continue
			}
			if tp, ok := child.(*ast.TextBlock); ok && !nodeHasImage(tp) {
				elements = c.collectInlineElements(tp)
				bodySeen = true
				continue
			}
		}
		descs, ids, imgs := c.convertContainerChild(child)
		descendants = append(descendants, descs...)
		childIDs = append(childIDs, ids...)
		descImages = append(descImages, imgs...)
	}

	if len(elements) == 0 {
		elements = []*lark.DocxTextElement{emptyTextElement()}
	}

	blockText := &lark.DocxBlockText{Elements: elements}

	if isTodo {
		blockText.Style = &lark.DocxTextStyle{Done: done}
		id := c.idGen.next("todo")
		desc := &lark.DocxBlock{
			BlockID:   id,
			BlockType: lark.DocxBlockTypeTodo,
			Children:  childIDs,
			Todo:      blockText,
		}
		descendants = append([]*lark.DocxBlock{desc}, descendants...)
		return []string{id}, descendants, descImages
	}

	if list.IsOrdered() {
		id := c.idGen.next("ordered")
		desc := &lark.DocxBlock{
			BlockID:   id,
			BlockType: lark.DocxBlockTypeOrdered,
			Children:  childIDs,
			Ordered:   blockText,
		}
		descendants = append([]*lark.DocxBlock{desc}, descendants...)
		return []string{id}, descendants, descImages
	}

	id := c.idGen.next("bullet")
	desc := &lark.DocxBlock{
		BlockID:   id,
		BlockType: lark.DocxBlockTypeBullet,
		Children:  childIDs,
		Bullet:    blockText,
	}
	descendants = append([]*lark.DocxBlock{desc}, descendants...)
	return []string{id}, descendants, descImages
}

func (c *converter) checkTodo(item *ast.ListItem) (isTodo bool, done bool) {
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if p, ok := child.(*ast.Paragraph); ok {
			if fc := p.FirstChild(); fc != nil {
				if cb, ok := fc.(*east.TaskCheckBox); ok {
					return true, cb.IsChecked
				}
			}
			break
		}
		if tb, ok := child.(*ast.TextBlock); ok {
			if fc := tb.FirstChild(); fc != nil {
				if cb, ok := fc.(*east.TaskCheckBox); ok {
					return true, cb.IsChecked
				}
			}
			break
		}
	}
	return false, false
}

// convertContainerChild 将容器（列表项等）内的一个块级子节点转为 descendant 块，
// 返回 (后代块, 顶层 child IDs, 后代图片)。覆盖文本（多段落）/标题/代码块/分隔线/
// 数学块/图片/嵌套列表；未知块降级为递归其子节点。供 list 等容器复用。
func (c *converter) convertContainerChild(child ast.Node) ([]*lark.DocxBlock, []string, []DescendantImage) {
	var descendants []*lark.DocxBlock
	var childIDs []string
	var descImages []DescendantImage

	addText := func(elements []*lark.DocxTextElement) {
		if len(elements) == 0 {
			return
		}
		id := c.idGen.next("child_text")
		childIDs = append(childIDs, id)
		descendants = append(descendants, &lark.DocxBlock{
			BlockID:   id,
			BlockType: lark.DocxBlockTypeText,
			Text:      &lark.DocxBlockText{Elements: elements},
		})
	}

	switch ch := child.(type) {
	case *ast.Paragraph:
		if paragraphHasImage(ch) {
			return c.convertBlockquoteParagraphWithImages(ch)
		}
		addText(c.collectInlineElements(ch))
	case *ast.TextBlock:
		if nodeHasImage(ch) {
			return c.convertBlockquoteParagraphWithImages(ch)
		}
		addText(c.collectInlineElements(ch))
	case *ast.Heading:
		level := ch.Level
		if level < 1 {
			level = 1
		}
		if level > 9 {
			level = 9
		}
		id := c.idGen.next("child_heading")
		block := &lark.DocxBlock{BlockID: id, BlockType: lark.DocxBlockType(2 + level)}
		setHeadingField(block, level, &lark.DocxBlockText{Elements: c.collectInlineElements(ch)})
		childIDs = append(childIDs, id)
		descendants = append(descendants, block)
	case *ast.FencedCodeBlock:
		id := c.idGen.next("code")
		childIDs = append(childIDs, id)
		descendants = append(descendants, newCodeBlock(id, fencedCodeLang(ch, c.source), c.extractCodeBlockContent(ch)))
	case *ast.CodeBlock:
		id := c.idGen.next("code")
		childIDs = append(childIDs, id)
		descendants = append(descendants, newCodeBlock(id, "", c.extractCodeBlockContent(ch)))
	case *ast.ThematicBreak:
		id := c.idGen.next("divider")
		childIDs = append(childIDs, id)
		descendants = append(descendants, &lark.DocxBlock{
			BlockID:   id,
			BlockType: lark.DocxBlockTypeDivider,
			Divider:   &lark.DocxBlockDivider{},
		})
	case *MathBlock:
		if ch.Content != "" {
			addText([]*lark.DocxTextElement{{
				Equation: &lark.DocxTextElementEquation{Content: ch.Content},
			}})
		}
	case *ast.List:
		for sub := ch.FirstChild(); sub != nil; sub = sub.NextSibling() {
			if li, ok := sub.(*ast.ListItem); ok {
				subIDs, subDescs, subImgs := c.buildListItemDescendants(ch, li)
				childIDs = append(childIDs, subIDs...)
				descendants = append(descendants, subDescs...)
				descImages = append(descImages, subImgs...)
			}
		}
	case *ast.Blockquote:
		// 引用块 → callout([!TYPE]) 或 quote_container 子树
		var id string
		var descs []*lark.DocxBlock
		var imgs []DescendantImage
		if alertType := c.detectAlertType(ch); alertType != "" {
			id, descs, imgs = c.buildCalloutDescendants(ch, alertType)
		} else {
			id, descs, imgs = c.buildQuoteContainerDescendants(ch)
		}
		childIDs = append(childIDs, id)
		descendants = append(descendants, descs...)
		descImages = append(descImages, imgs...)
	case *east.Table:
		id, descs := c.buildTableDescendants(ch)
		childIDs = append(childIDs, id)
		descendants = append(descendants, descs...)
	default:
		// 不在预期内的子块类型（如 HTML 块）：暂无法保真转换，
		// 降级为递归提取其内容，并告警以便察觉潜在的格式丢失。
		log.Printf("警告: 列表项内暂不支持的子块类型 %T，已降级提取其内容（可能丢失该块格式）", child)
		if child.HasChildren() {
			for gc := child.FirstChild(); gc != nil; gc = gc.NextSibling() {
				d, i, im := c.convertContainerChild(gc)
				descendants = append(descendants, d...)
				childIDs = append(childIDs, i...)
				descImages = append(descImages, im...)
			}
		}
	}

	return descendants, childIDs, descImages
}

// --- Code Block ---

func (c *converter) convertFencedCodeBlock(n *ast.FencedCodeBlock) {
	lang := fencedCodeLang(n, c.source)
	content := c.extractCodeBlockContent(n)

	// Mermaid → AddOns block
	if strings.ToLower(lang) == "mermaid" {
		record, _ := json.Marshal(map[string]string{
			"data":  content,
			"theme": "default",
			"view":  "chart",
		})
		c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeAddOns,
			AddOns: &lark.DocxBlockAddOns{
				ComponentTypeID: addOnsMermaidComponentTypeID,
				Record:          string(record),
			},
		})
		return
	}

	// PlantUML → Board block (画板)
	if strings.ToLower(lang) == "plantuml" {
		idx := c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeBoard,
			Board:     &lark.DocxBlockBoard{},
		})
		c.result.BoardIndices = append(c.result.BoardIndices, idx)
		c.result.BoardCodes = append(c.result.BoardCodes, content)
		return
	}

	// 普通代码块
	c.addTopBlock(newCodeBlock("", lang, content))
}

// fencedCodeLang 提取 fenced code block 的语言（去掉 info 中的附加参数）。
func fencedCodeLang(n *ast.FencedCodeBlock, source []byte) string {
	if n.Info == nil {
		return ""
	}
	lang := strings.TrimSpace(string(n.Info.Segment.Value(source)))
	if idx := strings.IndexByte(lang, ' '); idx >= 0 {
		lang = lang[:idx]
	}
	return lang
}

// newCodeBlock 构造普通代码块 DocxBlock（不含 mermaid/plantuml 特殊处理）。
// id 为空用于顶层块；非空用于 descendant 子块（如列表项内的代码块）。
func newCodeBlock(id, lang, content string) *lark.DocxBlock {
	langID, ok := MdStr2DocxCodeLang[strings.ToLower(lang)]
	if !ok {
		langID = lark.DocxCodeLanguagePlainText
	}
	return &lark.DocxBlock{
		BlockID:   id,
		BlockType: lark.DocxBlockTypeCode,
		Code: &lark.DocxBlockText{
			Style: &lark.DocxTextStyle{Language: langID, Wrap: true},
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: content},
			}},
		},
	}
}

func (c *converter) convertCodeBlock(n *ast.CodeBlock) {
	content := c.extractCodeBlockContent(n)
	c.addTopBlock(&lark.DocxBlock{
		BlockType: lark.DocxBlockTypeCode,
		Code: &lark.DocxBlockText{
			Style: &lark.DocxTextStyle{Language: lark.DocxCodeLanguagePlainText, Wrap: true},
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: content},
			}},
		},
	})
}

func (c *converter) extractCodeBlockContent(n ast.Node) string {
	var buf strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		buf.Write(seg.Value(c.source))
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// --- Blockquote / Callout ---

func (c *converter) convertBlockquote(n *ast.Blockquote) {
	// 检测是否为 GitHub Alert (Callout)
	alertType := c.detectAlertType(n)
	if alertType != "" {
		c.convertCallout(n, alertType)
		return
	}

	// 普通引用 → QuoteContainer (descendant)
	c.convertQuoteContainer(n)
}

func (c *converter) detectAlertType(n *ast.Blockquote) string {
	// 查找第一个 paragraph
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		p, ok := child.(*ast.Paragraph)
		if !ok {
			continue
		}
		// goldmark 将 [!NOTE] 解析为多个 text 节点: "[", "!NOTE", "]"
		// 拼接前几个 text 节点，检查是否匹配 [!TYPE]
		var buf strings.Builder
		for fc := p.FirstChild(); fc != nil; fc = fc.NextSibling() {
			t, ok := fc.(*ast.Text)
			if !ok {
				break
			}
			buf.Write(t.Segment.Value(c.source))
			text := strings.TrimSpace(buf.String())
			if strings.HasPrefix(text, "[!") && strings.HasSuffix(text, "]") {
				alertType := strings.ToUpper(text[2 : len(text)-1])
				if _, ok := alertTypeToCalloutColor[alertType]; ok {
					return alertType
				}
			}
			// 如果已经超过合理长度则停止
			if buf.Len() > 30 {
				break
			}
		}
		break
	}
	return ""
}

func (c *converter) convertCallout(n *ast.Blockquote, alertType string) {
	calloutID, descendants, descImages := c.buildCalloutDescendants(n, alertType)
	idx := c.addTopBlock(nil)
	c.result.DescendantGroups = append(c.result.DescendantGroups, DescendantGroup{
		TopBlockIndex:    idx,
		ChildrenIDs:      []string{calloutID},
		Descendants:      descendants,
		DescendantImages: descImages,
	})
}

// buildCalloutDescendants 构建 callout 子树（callout 块 + 其内容），返回根 ID。
// 供顶层 convertCallout 与容器子块（列表项内 [!TYPE] 引用）共用。
func (c *converter) buildCalloutDescendants(n *ast.Blockquote, alertType string) (string, []*lark.DocxBlock, []DescendantImage) {
	bgColor := alertTypeToCalloutColor[alertType]
	borderColor := alertTypeToBorderColor[alertType]

	calloutID := c.idGen.next("callout")
	var descendants []*lark.DocxBlock
	var childIDs []string
	var descImages []DescendantImage

	// 跳过包含 [!TYPE] 的第一个段落的 alert 标记行
	firstParagraph := true
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if p, ok := child.(*ast.Paragraph); ok && firstParagraph {
			firstParagraph = false
			// 收集该段落中除 [!TYPE] 标记外的内容
			elements := c.collectCalloutFirstParagraph(p)
			if len(elements) > 0 {
				contentID := c.idGen.next("callout_text")
				childIDs = append(childIDs, contentID)
				descendants = append(descendants, &lark.DocxBlock{
					BlockID:   contentID,
					BlockType: lark.DocxBlockTypeText,
					Text:      &lark.DocxBlockText{Elements: elements},
				})
			}
			continue
		}
		firstParagraph = false

		// 其他子块
		descs, ids, imgs := c.convertBlockquoteChild(child)
		childIDs = append(childIDs, ids...)
		descendants = append(descendants, descs...)
		descImages = append(descImages, imgs...)
	}

	calloutBlock := &lark.DocxBlock{
		BlockID:   calloutID,
		BlockType: lark.DocxBlockTypeCallout,
		Children:  childIDs,
		Callout: &lark.DocxBlockCallout{
			BackgroundColor: bgColor,
			BorderColor:     borderColor,
		},
	}
	return calloutID, append([]*lark.DocxBlock{calloutBlock}, descendants...), descImages
}

func (c *converter) collectCalloutFirstParagraph(p *ast.Paragraph) []*lark.DocxTextElement {
	// 跳过组成 [!TYPE] 标记的 text 节点
	// goldmark 将 [!NOTE] 解析为多个 text: "[", "!NOTE", "]"
	var elements []*lark.DocxTextElement
	skipMode := true
	var accumulated strings.Builder
	for child := p.FirstChild(); child != nil; child = child.NextSibling() {
		if skipMode {
			if t, ok := child.(*ast.Text); ok {
				accumulated.Write(t.Segment.Value(c.source))
				text := strings.TrimSpace(accumulated.String())
				if strings.HasPrefix(text, "[!") && strings.HasSuffix(text, "]") {
					skipMode = false
					continue
				}
				if accumulated.Len() > 30 {
					// 不是 alert 标记，回退处理所有
					skipMode = false
					// 重新从头收集
					elements = nil
					for ch := p.FirstChild(); ch != nil; ch = ch.NextSibling() {
						c.walkInline(ch, &lark.DocxTextElementStyle{}, &elements)
					}
					return elements
				}
				continue
			}
			skipMode = false
		}
		c.walkInline(child, &lark.DocxTextElementStyle{}, &elements)
	}
	return elements
}

func (c *converter) convertQuoteContainer(n *ast.Blockquote) {
	quoteID, descendants, descImages := c.buildQuoteContainerDescendants(n)
	idx := c.addTopBlock(nil)
	c.result.DescendantGroups = append(c.result.DescendantGroups, DescendantGroup{
		TopBlockIndex:    idx,
		ChildrenIDs:      []string{quoteID},
		Descendants:      descendants,
		DescendantImages: descImages,
	})
}

// buildQuoteContainerDescendants 构建 quote_container 子树（容器块 + 其内容），返回根 ID。
// 供顶层 convertQuoteContainer 与容器子块（列表项内引用块）共用。
func (c *converter) buildQuoteContainerDescendants(n *ast.Blockquote) (string, []*lark.DocxBlock, []DescendantImage) {
	quoteID := c.idGen.next("quote")
	var descendants []*lark.DocxBlock
	var childIDs []string
	var descImages []DescendantImage

	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		descs, ids, imgs := c.convertBlockquoteChild(child)
		childIDs = append(childIDs, ids...)
		descendants = append(descendants, descs...)
		descImages = append(descImages, imgs...)
	}

	quoteBlock := &lark.DocxBlock{
		BlockID:        quoteID,
		BlockType:      lark.DocxBlockTypeQuoteContainer,
		Children:       childIDs,
		QuoteContainer: &lark.DocxBlockQuoteContainer{},
	}
	return quoteID, append([]*lark.DocxBlock{quoteBlock}, descendants...), descImages
}

func (c *converter) convertBlockquoteChild(child ast.Node) ([]*lark.DocxBlock, []string, []DescendantImage) {
	var descendants []*lark.DocxBlock
	var childIDs []string
	var descImages []DescendantImage

	switch ch := child.(type) {
	case *ast.Paragraph:
		if paragraphHasImage(ch) {
			descendants, childIDs, descImages = c.convertBlockquoteParagraphWithImages(ch)
		} else {
			elements := c.collectInlineElements(ch)
			if len(elements) > 0 {
				id := c.idGen.next("bq_text")
				childIDs = append(childIDs, id)
				descendants = append(descendants, &lark.DocxBlock{
					BlockID:   id,
					BlockType: lark.DocxBlockTypeText,
					Text:      &lark.DocxBlockText{Elements: elements},
				})
			}
		}
	case *ast.Heading:
		level := ch.Level
		if level < 1 {
			level = 1
		}
		if level > 9 {
			level = 9
		}
		elements := c.collectInlineElements(ch)
		blockText := &lark.DocxBlockText{Elements: elements}
		id := c.idGen.next("bq_heading")
		block := &lark.DocxBlock{
			BlockID:   id,
			BlockType: lark.DocxBlockType(2 + level),
		}
		setHeadingField(block, level, blockText)
		childIDs = append(childIDs, id)
		descendants = append(descendants, block)
	case *ast.List:
		for item := ch.FirstChild(); item != nil; item = item.NextSibling() {
			if li, ok := item.(*ast.ListItem); ok {
				ids, descs, imgs := c.buildListItemDescendants(ch, li)
				childIDs = append(childIDs, ids...)
				descendants = append(descendants, descs...)
				descImages = append(descImages, imgs...)
			}
		}
	case *ast.FencedCodeBlock:
		lang := ""
		if ch.Info != nil {
			lang = strings.TrimSpace(string(ch.Info.Segment.Value(c.source)))
		}
		langID, ok := MdStr2DocxCodeLang[strings.ToLower(lang)]
		if !ok {
			langID = lark.DocxCodeLanguagePlainText
		}
		content := c.extractCodeBlockContent(ch)
		id := c.idGen.next("bq_code")
		childIDs = append(childIDs, id)
		descendants = append(descendants, &lark.DocxBlock{
			BlockID:   id,
			BlockType: lark.DocxBlockTypeCode,
			Code: &lark.DocxBlockText{
				Style: &lark.DocxTextStyle{Language: langID, Wrap: true},
				Elements: []*lark.DocxTextElement{{
					TextRun: &lark.DocxTextElementTextRun{Content: content},
				}},
			},
		})
	default:
		// 其他类型当作文本处理
		if child.HasChildren() {
			for gc := child.FirstChild(); gc != nil; gc = gc.NextSibling() {
				subDescs, subIDs, subImgs := c.convertBlockquoteChild(gc)
				childIDs = append(childIDs, subIDs...)
				descendants = append(descendants, subDescs...)
				descImages = append(descImages, subImgs...)
			}
		}
	}

	return descendants, childIDs, descImages
}

// nodeHasImage 检查节点直接子节点中是否包含 *ast.Image
func nodeHasImage(n ast.Node) bool {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if _, ok := ch.(*ast.Image); ok {
			return true
		}
	}
	return false
}

// paragraphHasImage 检查段落直接子节点中是否包含 *ast.Image
func paragraphHasImage(p *ast.Paragraph) bool {
	return nodeHasImage(p)
}

// convertBlockquoteParagraphWithImages 处理 blockquote 内含图片的段落，
// 将段落拆分为 text + image + text 序列（与飞书原生结构一致）
func (c *converter) convertBlockquoteParagraphWithImages(ch ast.Node) ([]*lark.DocxBlock, []string, []DescendantImage) {
	var descendants []*lark.DocxBlock
	var childIDs []string
	var descImages []DescendantImage
	var elements []*lark.DocxTextElement
	var htmlStyleStack []htmlStyleEntry

	flushText := func() {
		if len(elements) == 0 {
			return
		}
		id := c.idGen.next("bq_text")
		childIDs = append(childIDs, id)
		descendants = append(descendants, &lark.DocxBlock{
			BlockID:   id,
			BlockType: lark.DocxBlockTypeText,
			Text:      &lark.DocxBlockText{Elements: elements},
		})
		elements = nil
	}

	for n := ch.FirstChild(); n != nil; n = n.NextSibling() {
		if c.handleCiteInline(n, &elements) {
			continue
		}
		if img, ok := n.(*ast.Image); ok {
			flushText()
			id := c.idGen.next("bq_img")
			childIDs = append(childIDs, id)
			descendants = append(descendants, &lark.DocxBlock{
				BlockID:   id,
				BlockType: lark.DocxBlockTypeImage,
				Image:     &lark.DocxBlockImage{Token: ""},
			})
			descImages = append(descImages, DescendantImage{
				TempBlockID: id,
				ImagePath:   string(img.Destination),
			})
			continue
		}
		if raw, ok := n.(*ast.RawHTML); ok {
			tag := string(raw.Segments.Value(c.source))
			c.handleInlineHTMLTag(tag, &htmlStyleStack, &elements)
			continue
		}
		if tag := findSubSup(htmlStyleStack); tag != "" {
			text := extractInlineText(n, c.source)
			if text != "" {
				prefix := "_"
				if tag == "sup" {
					prefix = "^"
				}
				elements = append(elements, &lark.DocxTextElement{
					Equation: &lark.DocxTextElementEquation{
						Content: prefix + "{" + text + "}",
					},
				})
			}
			continue
		}
		baseStyle := mergeHTMLStyles(htmlStyleStack)
		c.walkInline(n, baseStyle, &elements)
	}
	c.flushCite(&elements) // 兜底：未闭合的 <cite>
	flushText()

	return descendants, childIDs, descImages
}

// --- Image ---

func (c *converter) convertImage(n *ast.Image) {
	idx := c.addTopBlock(&lark.DocxBlock{
		BlockType: lark.DocxBlockTypeImage,
		Image:     &lark.DocxBlockImage{Token: ""},
	})
	c.result.ImageIndices = append(c.result.ImageIndices, idx)
	c.result.ImagePaths = append(c.result.ImagePaths, string(n.Destination))
}

// --- Math Block ---

func (c *converter) convertMathBlock(n *MathBlock) {
	if n.Content == "" {
		return
	}
	c.addTopBlock(&lark.DocxBlock{
		BlockType: lark.DocxBlockTypeText,
		Text: &lark.DocxBlockText{
			Elements: []*lark.DocxTextElement{{
				Equation: &lark.DocxTextElementEquation{Content: n.Content},
			}},
		},
	})
}

// --- Table ---

func (c *converter) convertTable(n *east.Table) {
	tableID, descendants := c.buildTableDescendants(n)
	idx := c.addTopBlock(nil) // 占位
	c.result.DescendantGroups = append(c.result.DescendantGroups, DescendantGroup{
		TopBlockIndex: idx,
		ChildrenIDs:   []string{tableID},
		Descendants:   descendants,
	})
}

// buildTableDescendants 构建 table 子树（table 块 + 各 cell/content），返回根 ID。
// 供顶层 convertTable 与容器子块（列表项内表格）共用。
func (c *converter) buildTableDescendants(n *east.Table) (string, []*lark.DocxBlock) {
	rows, cols := countTableDimensions(n)
	tableID := c.idGen.next("table")

	var descendants []*lark.DocxBlock
	var cellIDs []string
	colMaxWidths := make([]int, cols)

	row := 0
	for tr := n.FirstChild(); tr != nil; tr = tr.NextSibling() {
		col := 0
		for td := tr.FirstChild(); td != nil; td = td.NextSibling() {
			cellID := fmt.Sprintf("cell_%s_%d_%d", tableID, row, col)
			contentID := fmt.Sprintf("content_%s_%d_%d", tableID, row, col)

			elements := c.collectInlineElements(td)
			if len(elements) == 0 {
				elements = []*lark.DocxTextElement{emptyTextElement()}
			}

			if w := displayWidth(extractNodeText(td, c.source)); w > colMaxWidths[col] {
				colMaxWidths[col] = w
			}

			descendants = append(descendants,
				&lark.DocxBlock{
					BlockID:   cellID,
					BlockType: lark.DocxBlockTypeTableCell,
					TableCell: &lark.DocxBlockTableCell{},
					Children:  []string{contentID},
				},
				&lark.DocxBlock{
					BlockID:   contentID,
					BlockType: lark.DocxBlockTypeText,
					Text:      &lark.DocxBlockText{Elements: elements},
				},
			)
			cellIDs = append(cellIDs, cellID)
			col++
		}
		// 补齐不足的列
		for col < cols {
			cellID := fmt.Sprintf("cell_%s_%d_%d", tableID, row, col)
			contentID := fmt.Sprintf("content_%s_%d_%d", tableID, row, col)
			descendants = append(descendants,
				&lark.DocxBlock{
					BlockID:   cellID,
					BlockType: lark.DocxBlockTypeTableCell,
					TableCell: &lark.DocxBlockTableCell{},
					Children:  []string{contentID},
				},
				&lark.DocxBlock{
					BlockID:   contentID,
					BlockType: lark.DocxBlockTypeText,
					Text:      &lark.DocxBlockText{Elements: []*lark.DocxTextElement{emptyTextElement()}},
				},
			)
			cellIDs = append(cellIDs, cellID)
			col++
		}
		row++
	}

	tableBlock := &lark.DocxBlock{
		BlockID:   tableID,
		BlockType: lark.DocxBlockTypeTable,
		Table: &lark.DocxBlockTable{
			Cells: cellIDs,
			Property: &lark.DocxBlockTableProperty{
				RowSize:     int64(rows),
				ColumnSize:  int64(cols),
				ColumnWidth: calcColumnWidths(colMaxWidths, tablePageWidth),
				HeaderRow:   true,
			},
		},
		Children: cellIDs,
	}
	return tableID, append([]*lark.DocxBlock{tableBlock}, descendants...)
}

func countTableDimensions(n *east.Table) (rows, cols int) {
	for tr := n.FirstChild(); tr != nil; tr = tr.NextSibling() {
		rows++
		colCount := 0
		for td := tr.FirstChild(); td != nil; td = td.NextSibling() {
			colCount++
		}
		if colCount > cols {
			cols = colCount
		}
	}
	return
}

// =============================================================
// Inline element collection
// =============================================================

func (c *converter) collectInlineElements(parent ast.Node) []*lark.DocxTextElement {
	var elements []*lark.DocxTextElement
	var htmlStyleStack []htmlStyleEntry

	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if c.handleCiteInline(child, &elements) {
			continue
		}
		if raw, ok := child.(*ast.RawHTML); ok {
			tag := string(raw.Segments.Value(c.source))
			c.handleInlineHTMLTag(tag, &htmlStyleStack, &elements)
			continue
		}
		if tag := findSubSup(htmlStyleStack); tag != "" {
			text := extractInlineText(child, c.source)
			if text != "" {
				prefix := "_"
				if tag == "sup" {
					prefix = "^"
				}
				elements = append(elements, &lark.DocxTextElement{
					Equation: &lark.DocxTextElementEquation{
						Content: prefix + "{" + text + "}",
					},
				})
			}
			continue
		}
		baseStyle := mergeHTMLStyles(htmlStyleStack)
		c.walkInline(child, baseStyle, &elements)
	}
	c.flushCite(&elements) // 兜底：未闭合的 <cite>
	return elements
}

// handleInlineHTMLTag 处理 inline HTML 标签，更新样式栈或产生元素
func (c *converter) handleInlineHTMLTag(raw string, stack *[]htmlStyleEntry, elements *[]*lark.DocxTextElement) {
	tagName, isClose, attrs := parseHTMLTag(raw)
	if tagName == "" {
		return
	}

	// <br> 自闭合标签：产生换行
	if tagName == "br" {
		*elements = append(*elements, &lark.DocxTextElement{
			TextRun: &lark.DocxTextElementTextRun{Content: "\n"},
		})
		return
	}

	if !isInlineStyleTag(tagName) {
		return
	}

	if isClose {
		// 弹出栈中最近的同名标签
		for i := len(*stack) - 1; i >= 0; i-- {
			if (*stack)[i].tag == tagName {
				*stack = append((*stack)[:i], (*stack)[i+1:]...)
				break
			}
		}
		return
	}

	// 开标签：入栈
	entry := htmlStyleEntry{tag: tagName}
	switch tagName {
	case "a":
		entry.href = attrs["href"]
	case "span":
		if style, ok := attrs["style"]; ok {
			entry.textColor, entry.bgColor = parseCSSStyleColors(style)
		}
	case "font":
		if color, ok := attrs["color"]; ok {
			entry.textColor = cssColorToDocxFontColor(color)
		}
	}
	*stack = append(*stack, entry)
}

func (c *converter) walkInline(node ast.Node, style *lark.DocxTextElementStyle, elements *[]*lark.DocxTextElement) {
	switch n := node.(type) {
	case *ast.Text:
		text := string(n.Segment.Value(c.source))
		if n.SoftLineBreak() {
			text += "\n"
		}
		if text != "" {
			*elements = append(*elements, &lark.DocxTextElement{
				TextRun: &lark.DocxTextElementTextRun{
					Content:          text,
					TextElementStyle: cloneStyle(style),
				},
			})
		}
	case *ast.String:
		text := string(n.Value)
		if text != "" {
			*elements = append(*elements, &lark.DocxTextElement{
				TextRun: &lark.DocxTextElementTextRun{
					Content:          text,
					TextElementStyle: cloneStyle(style),
				},
			})
		}
	case *ast.Emphasis:
		newStyle := cloneStyle(style)
		if n.Level == 2 {
			newStyle.Bold = true
		} else {
			newStyle.Italic = true
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			c.walkInline(child, newStyle, elements)
		}
	case *ast.CodeSpan:
		newStyle := cloneStyle(style)
		newStyle.InlineCode = true
		text := extractCodeSpanText(n, c.source)
		*elements = append(*elements, &lark.DocxTextElement{
			TextRun: &lark.DocxTextElementTextRun{
				Content:          text,
				TextElementStyle: newStyle,
			},
		})
	case *ast.Link:
		newStyle := cloneStyle(style)
		dest := string(n.Destination)
		if isValidLinkURL(dest) {
			newStyle.Link = &lark.DocxTextElementStyleLink{
				URL: dest,
			}
		}
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			c.walkInline(child, newStyle, elements)
		}
	case *ast.Image:
		// inline image → 不在 inline context 处理，
		// 但记录其 alt 文本作为占位
		var altBuf strings.Builder
		for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
			if t, ok := ch.(*ast.Text); ok {
				altBuf.Write(t.Segment.Value(c.source))
			}
		}
		alt := altBuf.String()
		if alt == "" {
			alt = string(n.Destination)
		}
		*elements = append(*elements, &lark.DocxTextElement{
			TextRun: &lark.DocxTextElementTextRun{
				Content:          alt,
				TextElementStyle: cloneStyle(style),
			},
		})
	case *ast.AutoLink:
		dest := string(n.URL(c.source))
		newStyle := cloneStyle(style)
		if isValidLinkURL(dest) {
			newStyle.Link = &lark.DocxTextElementStyleLink{URL: dest}
		}
		*elements = append(*elements, &lark.DocxTextElement{
			TextRun: &lark.DocxTextElementTextRun{
				Content:          dest,
				TextElementStyle: newStyle,
			},
		})
	case *InlineMath:
		*elements = append(*elements, &lark.DocxTextElement{
			Equation: &lark.DocxTextElementEquation{
				Content: n.Content,
			},
		})
	case *east.Strikethrough:
		newStyle := cloneStyle(style)
		newStyle.Strikethrough = true
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			c.walkInline(child, newStyle, elements)
		}
	case *east.TaskCheckBox:
		// 由 convertFlatListItem / checkTodo 处理，这里跳过
	default:
		// 尝试递归子节点
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			c.walkInline(child, style, elements)
		}
	}
}

func cloneStyle(s *lark.DocxTextElementStyle) *lark.DocxTextElementStyle {
	if s == nil {
		return &lark.DocxTextElementStyle{}
	}
	clone := *s
	if s.Link != nil {
		linkCopy := *s.Link
		clone.Link = &linkCopy
	}
	return &clone
}

func extractCodeSpanText(n *ast.CodeSpan, source []byte) string {
	var buf strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			buf.Write(t.Segment.Value(source))
		}
	}
	return buf.String()
}

// --- HTMLBlock (<details>/<summary>) ---

var detailsSummaryRe = regexp.MustCompile(`(?is)<details[^>]*>\s*<summary>(.*?)</summary>`)

func (c *converter) convertHTMLBlock(n *ast.HTMLBlock) {
	raw := c.nodeLines(n)

	if summary, ok := extractDetailsSummary(raw); ok {
		c.beginDetailsCapture(summary)
		return
	}

	if isDetailsClose(raw) {
		if len(c.detailsStack) > 0 {
			c.endDetailsCapture()
		}
		return
	}

	// 通用 HTML 块解析
	c.convertGenericHTMLBlock(raw)
}

func (c *converter) convertGenericHTMLBlock(raw string) {
	doc, err := parseHTMLFragment(raw)
	if err != nil {
		return
	}
	body := findBody(doc)
	if body == nil {
		return
	}
	c.walkHTMLChildrenGrouped(body)
}

func (c *converter) nodeLines(n ast.Node) string {
	var buf strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		buf.Write(seg.Value(c.source))
	}
	return buf.String()
}

func extractDetailsSummary(html string) (string, bool) {
	m := detailsSummaryRe.FindStringSubmatch(html)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func isDetailsClose(html string) bool {
	return strings.TrimSpace(html) == "</details>"
}

// beginDetailsCapture 遇到 <details><summary>text</summary> 时，
// 记录当前 result 各 slice 的长度快照，后续块正常转换，endDetailsCapture 时收集。
func (c *converter) beginDetailsCapture(summary string) {
	c.detailsStack = append(c.detailsStack, &detailsCapture{
		summary:       summary,
		startTopIdx:   len(c.result.TopBlocks),
		startDGIdx:    len(c.result.DescendantGroups),
		startImgIdx:   len(c.result.ImageIndices),
		startFileIdx:  len(c.result.FileIndices),
		startBoardIdx: len(c.result.BoardIndices),
	})
}

// endDetailsCapture 遇到 </details> 时，将快照之后新增的所有块
// 收集为一个 DescendantGroup（Text block with folded=true + children）。
func (c *converter) endDetailsCapture() {
	dc := c.detailsStack[len(c.detailsStack)-1]
	c.detailsStack = c.detailsStack[:len(c.detailsStack)-1]

	// 提取 captured 的块和资源
	capturedBlocks := append([]*lark.DocxBlock(nil), c.result.TopBlocks[dc.startTopIdx:]...)
	capturedDGs := append([]DescendantGroup(nil), c.result.DescendantGroups[dc.startDGIdx:]...)
	capturedImgIdxs := append([]int(nil), c.result.ImageIndices[dc.startImgIdx:]...)
	capturedImgPaths := append([]string(nil), c.result.ImagePaths[dc.startImgIdx:]...)
	capturedFileIdxs := append([]int(nil), c.result.FileIndices[dc.startFileIdx:]...)
	capturedFilePaths := append([]string(nil), c.result.FilePaths[dc.startFileIdx:]...)
	capturedBoardIdxs := append([]int(nil), c.result.BoardIndices[dc.startBoardIdx:]...)
	capturedBoardCodes := append([]string(nil), c.result.BoardCodes[dc.startBoardIdx:]...)

	// 截断 result 回到快照位置
	c.result.TopBlocks = c.result.TopBlocks[:dc.startTopIdx]
	c.result.DescendantGroups = c.result.DescendantGroups[:dc.startDGIdx]
	c.result.ImageIndices = c.result.ImageIndices[:dc.startImgIdx]
	c.result.ImagePaths = c.result.ImagePaths[:dc.startImgIdx]
	c.result.FileIndices = c.result.FileIndices[:dc.startFileIdx]
	c.result.FilePaths = c.result.FilePaths[:dc.startFileIdx]
	c.result.BoardIndices = c.result.BoardIndices[:dc.startBoardIdx]
	c.result.BoardCodes = c.result.BoardCodes[:dc.startBoardIdx]

	// 构建 captured DG 的索引映射（旧 TopBlockIndex → DescendantGroup）
	dgByOldIdx := make(map[int]*DescendantGroup, len(capturedDGs))
	for i := range capturedDGs {
		dgByOldIdx[capturedDGs[i].TopBlockIndex] = &capturedDGs[i]
	}

	// 构建 children 关系
	parentID := c.idGen.next("details")
	var descendants []*lark.DocxBlock
	var childIDs []string
	var descImages []DescendantImage

	for i, block := range capturedBlocks {
		oldIdx := dc.startTopIdx + i
		if block == nil {
			// DescendantGroup placeholder：合并其 children 和 descendants
			if dg, ok := dgByOldIdx[oldIdx]; ok {
				childIDs = append(childIDs, dg.ChildrenIDs...)
				descendants = append(descendants, dg.Descendants...)
				descImages = append(descImages, dg.DescendantImages...)
			}
		} else {
			// 普通块：分配 temp ID，作为子块
			tempID := c.idGen.next("details_child")
			block.BlockID = tempID
			childIDs = append(childIDs, tempID)
			descendants = append(descendants, block)
		}
	}

	// 将 captured images 转为 DescendantImage
	for j, imgIdx := range capturedImgIdxs {
		relIdx := imgIdx - dc.startTopIdx
		if relIdx >= 0 && relIdx < len(capturedBlocks) && capturedBlocks[relIdx] != nil {
			descImages = append(descImages, DescendantImage{
				TempBlockID: capturedBlocks[relIdx].BlockID,
				ImagePath:   capturedImgPaths[j],
			})
		}
	}

	// captured files 和 boards 暂不支持在 details 内（罕见场景），忽略
	_ = capturedFileIdxs
	_ = capturedFilePaths
	_ = capturedBoardIdxs
	_ = capturedBoardCodes

	// 创建父 Text block（folded=true）
	parentBlock := &lark.DocxBlock{
		BlockID:   parentID,
		BlockType: lark.DocxBlockTypeText,
		Children:  childIDs,
		Text: &lark.DocxBlockText{
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: dc.summary},
			}},
			Style: &lark.DocxTextStyle{Folded: true},
		},
	}
	descendants = append([]*lark.DocxBlock{parentBlock}, descendants...)

	// 添加为 DescendantGroup
	idx := c.addTopBlock(nil)
	c.result.DescendantGroups = append(c.result.DescendantGroups, DescendantGroup{
		TopBlockIndex:    idx,
		ChildrenIDs:      []string{parentID},
		Descendants:      descendants,
		DescendantImages: descImages,
	})
}

// isLocalFile 判断链接目标是否为本地文件
// 排除 URL scheme（http/https/mailto/tel/#/data:）后，检查文件是否存在于 mdDir 下
func (c *converter) isLocalFile(dest string) bool {
	if c.mdDir == "" {
		return false
	}
	lower := strings.ToLower(dest)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(dest, "#") || strings.HasPrefix(lower, "data:") {
		return false
	}
	path := dest
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.mdDir, path)
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// isValidLinkURL 判断链接 URL 是否可被飞书 link.url 接受（必须带 scheme，
// 相对路径/锚点会触发 1770006 schema mismatch）
func isValidLinkURL(dest string) bool {
	u, err := url.Parse(dest)
	return err == nil && u.Scheme != ""
}

// extractLinkText 提取链接的文本内容
func extractLinkText(link *ast.Link, source []byte) string {
	var buf strings.Builder
	for ch := link.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if t, ok := ch.(*ast.Text); ok {
			buf.Write(t.Segment.Value(source))
		}
	}
	return buf.String()
}

// extractNodeText 递归提取 goldmark AST 节点的纯文本内容
func extractNodeText(n ast.Node, source []byte) string {
	var buf strings.Builder
	ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := node.(*ast.Text); ok {
			buf.Write(t.Segment.Value(source))
		}
		return ast.WalkContinue, nil
	})
	return buf.String()
}
