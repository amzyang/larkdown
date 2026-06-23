package core

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/chyroc/lark"
	"github.com/yuin/goldmark/ast"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlStyleEntry 表示 HTML 样式栈中的一个条目
type htmlStyleEntry struct {
	tag       string                       // 标签名（小写）
	href      string                       // 仅 <a> 标签使用
	textColor lark.DocxFontColor           // <span style="color:..."> 或 <font color="...">
	bgColor   lark.DocxFontBackgroundColor // <span style="background-color:...">
}

// isInlineStyleTag 判断是否为影响文本样式的 inline 标签
func isInlineStyleTag(tag string) bool {
	switch tag {
	case "u", "ins", "b", "strong", "i", "em", "del", "s", "strike",
		"code", "mark", "a", "sub", "sup", "kbd", "span", "font":
		return true
	}
	return false
}

// parseHTMLTag 从 raw HTML 片段中解析标签名和属性。
// 返回标签名（小写）、是否为闭合标签、属性映射。
func parseHTMLTag(raw string) (tagName string, isClose bool, attrs map[string]string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}

	r := strings.NewReader(raw)
	z := html.NewTokenizer(r)
	tt := z.Next()
	switch tt {
	case html.StartTagToken, html.SelfClosingTagToken:
		tn, hasAttr := z.TagName()
		tagName = string(tn)
		attrs = make(map[string]string)
		for hasAttr {
			var key, val []byte
			key, val, hasAttr = z.TagAttr()
			attrs[string(key)] = string(val)
		}
		return tagName, false, attrs
	case html.EndTagToken:
		tn, _ := z.TagName()
		return string(tn), true, nil
	}
	return "", false, nil
}

// applyHTMLStyleToDocx 将 HTML 标签样式应用到 DocxTextElementStyle
func applyHTMLStyleToDocx(entry htmlStyleEntry, style *lark.DocxTextElementStyle) {
	switch entry.tag {
	case "u", "ins":
		style.Underline = true
	case "b", "strong":
		style.Bold = true
	case "i", "em":
		style.Italic = true
	case "del", "s", "strike":
		style.Strikethrough = true
	case "code", "kbd":
		style.InlineCode = true
	case "mark":
		style.BackgroundColor = lark.DocxFontBackgroundColorLightYellow
	case "a":
		if isValidLinkURL(entry.href) {
			style.Link = &lark.DocxTextElementStyleLink{URL: entry.href}
		}
	}
	if entry.textColor != 0 {
		style.TextColor = entry.textColor
	}
	if entry.bgColor != 0 {
		style.BackgroundColor = entry.bgColor
	}
}

// mergeHTMLStyles 将 HTML 样式栈合并到一个新的 DocxTextElementStyle
func mergeHTMLStyles(stack []htmlStyleEntry) *lark.DocxTextElementStyle {
	style := &lark.DocxTextElementStyle{}
	for _, entry := range stack {
		applyHTMLStyleToDocx(entry, style)
	}
	return style
}

// emptyTextElement 创建空内容的 TextElement（使用空格避免 omitempty 省略 content 字段）
func emptyTextElement() *lark.DocxTextElement {
	return &lark.DocxTextElement{
		TextRun: &lark.DocxTextElementTextRun{Content: " "},
	}
}

// --- Block-level HTML parsing ---

// htmlNewlineRe 匹配 HTML 中的换行+缩进空白（用于折叠为单空格）
var htmlNewlineRe = regexp.MustCompile(`\s*\n\s*`)

// normalizeHTMLWhitespace 折叠 HTML 格式化空白（换行+缩进 → 单空格）
func normalizeHTMLWhitespace(s string) string {
	return htmlNewlineRe.ReplaceAllString(s, " ")
}

// isInlineForGrouping 判断 HTML 元素是否为 inline 元素（非块级）
func isInlineForGrouping(n *html.Node) bool {
	if isInlineStyleTag(n.Data) {
		return true
	}
	switch n.DataAtom {
	case atom.Br, atom.Img, atom.Option:
		return true
	}
	return false
}

// walkHTMLChildrenGrouped 遍历 parent 的子节点，将相邻 inline 元素分组为单个 text block
func (c *converter) walkHTMLChildrenGrouped(parent *html.Node) {
	var inlineBuf []*lark.DocxTextElement

	flushInline := func() {
		if len(inlineBuf) == 0 {
			return
		}
		// trim 最后一个 element 的尾部空白
		last := inlineBuf[len(inlineBuf)-1]
		if last.TextRun != nil {
			last.TextRun.Content = strings.TrimRight(last.TextRun.Content, " \t")
		}
		// 过滤掉变为空的 element
		var filtered []*lark.DocxTextElement
		for _, e := range inlineBuf {
			if e.TextRun != nil && e.TextRun.Content == "" {
				continue
			}
			filtered = append(filtered, e)
		}
		if len(filtered) > 0 {
			c.addTopBlock(&lark.DocxBlock{
				BlockType: lark.DocxBlockTypeText,
				Text:      &lark.DocxBlockText{Elements: filtered},
			})
		}
		inlineBuf = nil
	}

	for child := parent.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			text := normalizeHTMLWhitespace(child.Data)
			if strings.TrimSpace(text) == "" {
				continue
			}
			inlineBuf = append(inlineBuf, &lark.DocxTextElement{
				TextRun: &lark.DocxTextElementTextRun{Content: text},
			})
		case html.ElementNode:
			if child.DataAtom == atom.Img {
				// <img> 作为独立 image block，不走 inline 路径
				flushInline()
				src := getAttr(child, "src")
				if src != "" {
					idx := c.addTopBlock(&lark.DocxBlock{
						BlockType: lark.DocxBlockTypeImage,
						Image:     &lark.DocxBlockImage{Token: ""},
					})
					c.result.ImageIndices = append(c.result.ImageIndices, idx)
					c.result.ImagePaths = append(c.result.ImagePaths, src)
				}
			} else if isInlineForGrouping(child) {
				c.appendInlineElement(child, &lark.DocxTextElementStyle{}, &inlineBuf)
			} else {
				flushInline()
				c.convertHTMLElement(child)
			}
		case html.DocumentNode:
			c.walkHTMLChildrenGrouped(child)
		}
	}
	flushInline()
}

// convertHTMLElement 处理单个 HTML 元素节点
func (c *converter) convertHTMLElement(n *html.Node) {
	// <whiteboard> 是 round-trip 白板的扩展标签（自定义标签，DataAtom=0），
	// 必须先按标签名拦截，否则会落入 default 分支被当作普通容器/文本。
	if n.Data == "whiteboard" {
		c.convertHTMLWhiteboard(n)
		return
	}

	switch n.DataAtom {
	case atom.Table:
		c.convertHTMLTable(n)
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		c.convertHTMLHeading(n)
	case atom.P:
		c.walkHTMLChildrenGrouped(n)
	case atom.Ul, atom.Ol:
		c.convertHTMLList(n)
	case atom.Blockquote:
		c.convertHTMLBlockquote(n)
	case atom.Pre:
		c.convertHTMLPre(n)
	case atom.Hr:
		c.addTopBlock(&lark.DocxBlock{
			BlockType: lark.DocxBlockTypeDivider,
			Divider:   &lark.DocxBlockDivider{},
		})
	case atom.Img:
		src := getAttr(n, "src")
		if src != "" {
			idx := c.addTopBlock(&lark.DocxBlock{
				BlockType: lark.DocxBlockTypeImage,
				Image:     &lark.DocxBlockImage{Token: ""},
			})
			c.result.ImageIndices = append(c.result.ImageIndices, idx)
			c.result.ImagePaths = append(c.result.ImagePaths, src)
		}
	case atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Footer, atom.Nav,
		atom.Form, atom.Fieldset, atom.Dl:
		// 容器元素：分组处理子节点
		c.walkHTMLChildrenGrouped(n)
	case atom.Dt:
		// <dt> 当作加粗文本
		elements := c.collectHTMLInlineElements(n)
		if len(elements) > 0 {
			for _, e := range elements {
				if e.TextRun != nil {
					if e.TextRun.TextElementStyle == nil {
						e.TextRun.TextElementStyle = &lark.DocxTextElementStyle{}
					}
					e.TextRun.TextElementStyle.Bold = true
				}
			}
			c.addTopBlock(&lark.DocxBlock{
				BlockType: lark.DocxBlockTypeText,
				Text:      &lark.DocxBlockText{Elements: elements},
			})
		}
	case atom.Dd:
		elements := c.collectHTMLInlineElements(n)
		if len(elements) > 0 {
			c.addTopBlock(&lark.DocxBlock{
				BlockType: lark.DocxBlockTypeText,
				Text:      &lark.DocxBlockText{Elements: elements},
			})
		}
	default:
		if isHTMLBlockElement(n) {
			c.walkHTMLChildrenGrouped(n)
		} else {
			// inline 元素作为文本块
			elements := c.collectHTMLInlineElements(n)
			if len(elements) > 0 {
				c.addTopBlock(&lark.DocxBlock{
					BlockType: lark.DocxBlockTypeText,
					Text:      &lark.DocxBlockText{Elements: elements},
				})
			}
		}
	}
}

// convertHTMLHeading 处理 HTML heading 标签
func (c *converter) convertHTMLHeading(n *html.Node) {
	level := int(n.DataAtom-atom.H1)/2 + 1 // h1=atom.H1, h2=atom.H2, ...
	// 通过实际标签名获取 level 更可靠
	if len(n.Data) == 2 && n.Data[0] == 'h' {
		level = int(n.Data[1] - '0')
	}
	if level < 1 {
		level = 1
	}
	if level > 9 {
		level = 9
	}

	elements := c.collectHTMLInlineElements(n)
	if len(elements) == 0 {
		return
	}

	blockText := &lark.DocxBlockText{Elements: elements}
	block := &lark.DocxBlock{
		BlockType: lark.DocxBlockType(2 + level),
	}
	setHeadingField(block, level, blockText)
	c.addTopBlock(block)
}

// convertHTMLList 处理 HTML ul/ol 列表
func (c *converter) convertHTMLList(n *html.Node) {
	isOrdered := n.DataAtom == atom.Ol
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || child.DataAtom != atom.Li {
			continue
		}
		c.convertHTMLListItem(child, isOrdered)
	}
}

// convertHTMLListItem 处理单个 <li>，分离 inline 内容与嵌套块级元素
func (c *converter) convertHTMLListItem(li *html.Node, isOrdered bool) {
	var inlineElements []*lark.DocxTextElement
	listItemEmitted := false

	// 遍历 <li> 的直接子节点
	for child := li.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			text := child.Data
			if text != "" {
				inlineElements = append(inlineElements, &lark.DocxTextElement{
					TextRun: &lark.DocxTextElementTextRun{Content: text},
				})
			}
			continue
		}
		if child.Type != html.ElementNode {
			continue
		}
		// 判断是否为块级元素
		if isHTMLBlockLevelForLi(child) {
			// 先输出之前积累的 inline 内容作为列表项
			if !listItemEmitted {
				c.emitHTMLListItem(inlineElements, isOrdered)
				listItemEmitted = true
				inlineElements = nil
			}
			// 块级元素作为独立 top-level block
			c.convertHTMLElement(child)
		} else {
			// inline 元素：应用自身样式并收集文本
			c.appendInlineElement(child, &lark.DocxTextElementStyle{}, &inlineElements)
		}
	}

	// 如果还没输出过列表项，输出积累的 inline 内容
	if !listItemEmitted {
		c.emitHTMLListItem(inlineElements, isOrdered)
	}
}

// emitHTMLListItem 输出一个列表项 block
func (c *converter) emitHTMLListItem(elements []*lark.DocxTextElement, isOrdered bool) {
	if len(elements) == 0 {
		elements = []*lark.DocxTextElement{emptyTextElement()}
	}
	blockText := &lark.DocxBlockText{Elements: elements}
	if isOrdered {
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

// isHTMLBlockLevelForLi 判断 <li> 子元素是否为块级元素（需要独立输出）
func isHTMLBlockLevelForLi(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Table, atom.P, atom.Ul, atom.Ol, atom.Blockquote, atom.Pre, atom.Hr,
		atom.Div, atom.Section, atom.Article, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		return true
	}
	return false
}

// convertHTMLBlockquote 处理 HTML blockquote
func (c *converter) convertHTMLBlockquote(n *html.Node) {
	quoteID := c.idGen.next("html_quote")
	var descendants []*lark.DocxBlock
	var childIDs []string

	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			text := strings.TrimSpace(child.Data)
			if text == "" {
				continue
			}
			id := c.idGen.next("html_bq_text")
			childIDs = append(childIDs, id)
			descendants = append(descendants, &lark.DocxBlock{
				BlockID:   id,
				BlockType: lark.DocxBlockTypeText,
				Text: &lark.DocxBlockText{
					Elements: []*lark.DocxTextElement{{
						TextRun: &lark.DocxTextElementTextRun{Content: text},
					}},
				},
			})
		} else if child.Type == html.ElementNode {
			if child.DataAtom == atom.P {
				elements := c.collectHTMLInlineElements(child)
				if len(elements) > 0 {
					id := c.idGen.next("html_bq_text")
					childIDs = append(childIDs, id)
					descendants = append(descendants, &lark.DocxBlock{
						BlockID:   id,
						BlockType: lark.DocxBlockTypeText,
						Text:      &lark.DocxBlockText{Elements: elements},
					})
				}
			}
		}
	}

	if len(childIDs) == 0 {
		return
	}

	quoteBlock := &lark.DocxBlock{
		BlockID:        quoteID,
		BlockType:      lark.DocxBlockTypeQuoteContainer,
		Children:       childIDs,
		QuoteContainer: &lark.DocxBlockQuoteContainer{},
	}
	descendants = append([]*lark.DocxBlock{quoteBlock}, descendants...)

	idx := c.addTopBlock(nil)
	c.result.DescendantGroups = append(c.result.DescendantGroups, DescendantGroup{
		TopBlockIndex: idx,
		ChildrenIDs:   []string{quoteID},
		Descendants:   descendants,
	})
}

// convertHTMLPre 处理 <pre> 标签（代码块）
func (c *converter) convertHTMLPre(n *html.Node) {
	// 查找 <pre><code> 模式
	var content string
	var lang string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.DataAtom == atom.Code {
			content = extractTextContent(child)
			// 尝试从 class 提取语言 (e.g., "language-go")
			cls := getAttr(child, "class")
			if l, ok := strings.CutPrefix(cls, "language-"); ok {
				lang = l
			}
			break
		}
	}
	if content == "" {
		content = extractTextContent(n)
	}
	content = strings.TrimSuffix(content, "\n")

	langID := lark.DocxCodeLanguagePlainText
	if lang != "" {
		if id, ok := MdStr2DocxCodeLang[strings.ToLower(lang)]; ok {
			langID = id
		}
	}

	c.addTopBlock(&lark.DocxBlock{
		BlockType: lark.DocxBlockTypeCode,
		Code: &lark.DocxBlockText{
			Style: &lark.DocxTextStyle{Language: langID},
			Elements: []*lark.DocxTextElement{{
				TextRun: &lark.DocxTextElementTextRun{Content: content},
			}},
		},
	})
}

// convertHTMLTable 处理 HTML table，支持 rowspan/colspan 展开
func (c *converter) convertHTMLTable(n *html.Node) {
	// 收集所有 <tr>
	var trs []*html.Node
	forEachDescendant(n, atom.Tr, func(tr *html.Node) {
		trs = append(trs, tr)
	})
	if len(trs) == 0 {
		return
	}

	// 第一遍：计算 grid 尺寸
	numRows := len(trs)
	numCols := 0
	// 用临时 grid 标记被占据的位置
	occupied := make([][]bool, numRows)
	for i := range occupied {
		occupied[i] = make([]bool, 64) // 初始容量，按需扩展
	}

	for ri, tr := range trs {
		col := 0
		for td := tr.FirstChild; td != nil; td = td.NextSibling {
			if td.Type != html.ElementNode || (td.DataAtom != atom.Td && td.DataAtom != atom.Th) {
				continue
			}
			// 跳过已被 rowspan 占据的列
			for col < len(occupied[ri]) && occupied[ri][col] {
				col++
			}
			rs := attrInt(td, "rowspan", 1)
			cs := attrInt(td, "colspan", 1)
			// 标记占据的位置
			for dr := 0; dr < rs && ri+dr < numRows; dr++ {
				for dc := 0; dc < cs; dc++ {
					pos := col + dc
					// 动态扩展
					for pos >= len(occupied[ri+dr]) {
						occupied[ri+dr] = append(occupied[ri+dr], false)
					}
					occupied[ri+dr][pos] = true
				}
			}
			endCol := col + cs
			if endCol > numCols {
				numCols = endCol
			}
			col += cs
		}
	}

	if numCols == 0 {
		return
	}

	// 第二遍：构建 grid 内容矩阵（支持文本和图片）
	grid := make([][]cellContent, numRows)
	for i := range grid {
		grid[i] = make([]cellContent, numCols)
	}
	// 重置 occupied
	for i := range occupied {
		occupied[i] = make([]bool, numCols)
	}

	var mergeRegions []MergeRegion
	for ri, tr := range trs {
		col := 0
		for td := tr.FirstChild; td != nil; td = td.NextSibling {
			if td.Type != html.ElementNode || (td.DataAtom != atom.Td && td.DataAtom != atom.Th) {
				continue
			}
			for col < numCols && occupied[ri][col] {
				col++
			}
			if col >= numCols {
				break
			}
			rs := attrInt(td, "rowspan", 1)
			cs := attrInt(td, "colspan", 1)
			content := extractCellContent(td)
			// 仅原始单元格填充内容，被覆盖的位置留空
			grid[ri][col] = content
			for dr := 0; dr < rs && ri+dr < numRows; dr++ {
				for dc := 0; dc < cs && col+dc < numCols; dc++ {
					occupied[ri+dr][col+dc] = true
					if dr != 0 || dc != 0 {
						grid[ri+dr][col+dc] = cellContent{}
					}
				}
			}
			if rs > 1 || cs > 1 {
				rowEnd := int64(ri + rs)
				if rowEnd > int64(numRows) {
					rowEnd = int64(numRows)
				}
				colEnd := int64(col + cs)
				if colEnd > int64(numCols) {
					colEnd = int64(numCols)
				}
				mergeRegions = append(mergeRegions, MergeRegion{
					RowStartIndex:    int64(ri),
					RowEndIndex:      rowEnd,
					ColumnStartIndex: int64(col),
					ColumnEndIndex:   colEnd,
				})
			}
			col += cs
		}
	}

	// 计算每列最大内容宽度
	colMaxWidths := make([]int, numCols)
	for _, row := range grid {
		for ci, cell := range row {
			w := 0
			for _, seg := range cell.segments {
				w += displayWidth(seg.text)
			}
			if w > colMaxWidths[ci] {
				colMaxWidths[ci] = w
			}
		}
	}

	// 第三遍：生成 Descendants
	tableID := c.idGen.next("html_table")
	var descendants []*lark.DocxBlock
	var cellIDs []string
	var descImages []DescendantImage

	for _, row := range grid {
		for _, cell := range row {
			cellID := c.idGen.next("html_cell")
			var childIDs []string
			var childDescs []*lark.DocxBlock

			if len(cell.segments) == 0 {
				// 空单元格
				contentID := c.idGen.next("html_cell_content")
				childIDs = []string{contentID}
				childDescs = append(childDescs, &lark.DocxBlock{
					BlockID:   contentID,
					BlockType: lark.DocxBlockTypeText,
					Text:      &lark.DocxBlockText{Elements: []*lark.DocxTextElement{emptyTextElement()}},
				})
			} else {
				for _, seg := range cell.segments {
					if seg.imagePath != "" {
						imgID := c.idGen.next("html_cell_img")
						childIDs = append(childIDs, imgID)
						childDescs = append(childDescs, &lark.DocxBlock{
							BlockID:   imgID,
							BlockType: lark.DocxBlockTypeImage,
							Image:     &lark.DocxBlockImage{Token: ""},
						})
						descImages = append(descImages, DescendantImage{
							TempBlockID: imgID,
							ImagePath:   seg.imagePath,
						})
					} else {
						contentID := c.idGen.next("html_cell_content")
						childIDs = append(childIDs, contentID)
						text := seg.text
						var elements []*lark.DocxTextElement
						if text == "" {
							elements = []*lark.DocxTextElement{emptyTextElement()}
						} else {
							elements = []*lark.DocxTextElement{{
								TextRun: &lark.DocxTextElementTextRun{Content: text},
							}}
						}
						childDescs = append(childDescs, &lark.DocxBlock{
							BlockID:   contentID,
							BlockType: lark.DocxBlockTypeText,
							Text:      &lark.DocxBlockText{Elements: elements},
						})
					}
				}
			}

			// cell block 在前，children blocks 在后（API 要求 parent 在前）
			descendants = append(descendants, &lark.DocxBlock{
				BlockID:   cellID,
				BlockType: lark.DocxBlockTypeTableCell,
				TableCell: &lark.DocxBlockTableCell{},
				Children:  childIDs,
			})
			descendants = append(descendants, childDescs...)
			cellIDs = append(cellIDs, cellID)
		}
	}

	tableBlock := &lark.DocxBlock{
		BlockID:   tableID,
		BlockType: lark.DocxBlockTypeTable,
		Table: &lark.DocxBlockTable{
			Cells: cellIDs,
			Property: &lark.DocxBlockTableProperty{
				RowSize:     int64(numRows),
				ColumnSize:  int64(numCols),
				ColumnWidth: calcColumnWidths(colMaxWidths),
				HeaderRow:   true,
			},
		},
		Children: cellIDs,
	}
	descendants = append([]*lark.DocxBlock{tableBlock}, descendants...)

	idx := c.addTopBlock(nil)
	c.result.DescendantGroups = append(c.result.DescendantGroups, DescendantGroup{
		TopBlockIndex:    idx,
		ChildrenIDs:      []string{tableID},
		Descendants:      descendants,
		MergeRegions:     mergeRegions,
		DescendantImages: descImages,
	})
}

// attrInt 读取 HTML 节点的整数属性，默认返回 defaultVal
func attrInt(n *html.Node, key string, defaultVal int) int {
	s := getAttr(n, key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	return v
}

// collectHTMLInlineElements 从 HTML 节点收集 inline 元素，支持嵌套样式
func (c *converter) collectHTMLInlineElements(n *html.Node) []*lark.DocxTextElement {
	var elements []*lark.DocxTextElement
	c.walkHTMLInline(n, &lark.DocxTextElementStyle{}, &elements)
	return elements
}

// walkHTMLInline 递归遍历 HTML inline 内容
func (c *converter) walkHTMLInline(n *html.Node, style *lark.DocxTextElementStyle, elements *[]*lark.DocxTextElement) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			text := child.Data
			if text == "" {
				continue
			}
			*elements = append(*elements, &lark.DocxTextElement{
				TextRun: &lark.DocxTextElementTextRun{
					Content:          text,
					TextElementStyle: cloneStyle(style),
				},
			})
		case html.ElementNode:
			c.appendInlineElement(child, style, elements)
		}
	}
}

// appendInlineElement 处理单个 inline HTML 元素节点，应用样式并递归处理子节点
func (c *converter) appendInlineElement(n *html.Node, style *lark.DocxTextElementStyle, elements *[]*lark.DocxTextElement) {
	switch n.DataAtom {
	case atom.Br:
		*elements = append(*elements, &lark.DocxTextElement{
			TextRun: &lark.DocxTextElementTextRun{
				Content: "\n",
			},
		})
	case atom.Img:
		alt := getAttr(n, "alt")
		if alt == "" {
			alt = getAttr(n, "src")
		}
		if alt != "" {
			*elements = append(*elements, &lark.DocxTextElement{
				TextRun: &lark.DocxTextElementTextRun{
					Content:          alt,
					TextElementStyle: cloneStyle(style),
				},
			})
		}
	case atom.Sub, atom.Sup:
		text := extractTextContent(n)
		if text != "" {
			prefix := "_"
			if n.DataAtom == atom.Sup {
				prefix = "^"
			}
			*elements = append(*elements, &lark.DocxTextElement{
				Equation: &lark.DocxTextElementEquation{
					Content: prefix + "{" + text + "}",
				},
			})
		}
	default:
		newStyle := cloneStyle(style)
		entry := htmlStyleEntry{tag: n.Data}
		switch n.DataAtom {
		case atom.A:
			entry.href = getAttr(n, "href")
		case atom.Span:
			if styleAttr := getAttr(n, "style"); styleAttr != "" {
				entry.textColor, entry.bgColor = parseCSSStyleColors(styleAttr)
			}
		case atom.Font:
			if color := getAttr(n, "color"); color != "" {
				entry.textColor = cssColorToDocxFontColor(color)
			}
		}
		applyHTMLStyleToDocx(entry, newStyle)
		c.walkHTMLInline(n, newStyle, elements)
	}
}

// findSubSup 返回样式栈中第一个 "sub" 或 "sup" 标签名，无则返回 ""
func findSubSup(stack []htmlStyleEntry) string {
	for _, entry := range stack {
		if entry.tag == "sub" || entry.tag == "sup" {
			return entry.tag
		}
	}
	return ""
}

// extractInlineText 从 goldmark AST 节点提取纯文本内容
func extractInlineText(node ast.Node, source []byte) string {
	var buf strings.Builder
	extractInlineTextRecursive(node, source, &buf)
	return buf.String()
}

func extractInlineTextRecursive(node ast.Node, source []byte, buf *strings.Builder) {
	if t, ok := node.(*ast.Text); ok {
		buf.Write(t.Segment.Value(source))
		return
	}
	if s, ok := node.(*ast.String); ok {
		buf.Write(s.Value)
		return
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		extractInlineTextRecursive(child, source, buf)
	}
}

// --- Table cell content extraction ---

// cellSegment 表示单元格中的一段内容（文本或图片）
type cellSegment struct {
	text      string // 非空 = 文本段
	imagePath string // 非空 = 图片
}

// cellContent 表示单元格中的所有内容段
type cellContent struct {
	segments []cellSegment
}

// extractCellContent 从 HTML 节点提取单元格内容，区分文本和图片
func extractCellContent(n *html.Node) cellContent {
	var result cellContent
	var textBuf strings.Builder

	flushText := func() {
		if textBuf.Len() > 0 {
			result.segments = append(result.segments, cellSegment{text: textBuf.String()})
			textBuf.Reset()
		}
	}

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			textBuf.WriteString(node.Data)
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Img {
			flushText()
			src := getAttr(node, "src")
			if src != "" {
				result.segments = append(result.segments, cellSegment{imagePath: src})
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	flushText()
	return result
}

// --- 辅助函数 ---

// extractTextContent 提取 HTML 节点的纯文本内容
func extractTextContent(n *html.Node) string {
	var buf strings.Builder
	extractTextContentRecursive(n, &buf)
	return buf.String()
}

func extractTextContentRecursive(n *html.Node, buf *strings.Builder) {
	if n.Type == html.TextNode {
		buf.WriteString(n.Data)
		return
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		extractTextContentRecursive(child, buf)
	}
}

// getAttr 获取 HTML 节点的指定属性值
func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// forEachDescendant 在节点树中查找所有匹配的后代元素
func forEachDescendant(n *html.Node, a atom.Atom, fn func(*html.Node)) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.DataAtom == a {
			fn(child)
		}
		forEachDescendant(child, a, fn)
	}
}

// isHTMLBlockElement 判断是否为块级 HTML 元素
func isHTMLBlockElement(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Footer,
		atom.Nav, atom.Aside, atom.Figure, atom.Figcaption, atom.Details, atom.Summary,
		atom.Form, atom.Fieldset, atom.Label, atom.Textarea, atom.Select, atom.Input,
		atom.Dl, atom.Dt, atom.Dd, atom.Address:
		return true
	}
	return false
}

// parseHTMLFragment 使用 html.Parse 解析 HTML 片段
func parseHTMLFragment(raw string) (*html.Node, error) {
	return html.Parse(strings.NewReader(raw))
}

// findBody 从解析后的 html.Node 中找到 body 节点
func findBody(doc *html.Node) *html.Node {
	return findElement(doc, atom.Body)
}

// findElement 递归查找指定标签的元素
func findElement(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, a); found != nil {
			return found
		}
	}
	return nil
}

// --- CSS 颜色解析 ---

// cssNameToDocxFontColor CSS 命名颜色 → 飞书字体颜色
var cssNameToDocxFontColor = map[string]lark.DocxFontColor{
	"red":    lark.DocxFontColorLightPink,
	"pink":   lark.DocxFontColorLightPink,
	"orange": lark.DocxFontColorLightOrange,
	"yellow": lark.DocxFontColorLightYellow,
	"green":  lark.DocxFontColorLightGreen,
	"blue":   lark.DocxFontColorLightBlue,
	"purple": lark.DocxFontColorLightPurple,
	"violet": lark.DocxFontColorLightPurple,
	"gray":   lark.DocxFontColorLightGrey,
	"grey":   lark.DocxFontColorLightGrey,
}

// cssNameToDocxBgColor CSS 命名颜色 → 飞书背景色
var cssNameToDocxBgColor = map[string]lark.DocxFontBackgroundColor{
	"red":    lark.DocxFontBackgroundColorLightPink,
	"pink":   lark.DocxFontBackgroundColorLightPink,
	"orange": lark.DocxFontBackgroundColorLightOrange,
	"yellow": lark.DocxFontBackgroundColorLightYellow,
	"green":  lark.DocxFontBackgroundColorLightGreen,
	"blue":   lark.DocxFontBackgroundColorLightBlue,
	"purple": lark.DocxFontBackgroundColorLightPurple,
	"violet": lark.DocxFontBackgroundColorLightPurple,
	"gray":   lark.DocxFontBackgroundColorLightGrey,
	"grey":   lark.DocxFontBackgroundColorLightGrey,
}

// parseCSSStyleColors 从 CSS style 属性中提取 color 和 background-color
func parseCSSStyleColors(style string) (lark.DocxFontColor, lark.DocxFontBackgroundColor) {
	var textColor lark.DocxFontColor
	var bgColor lark.DocxFontBackgroundColor
	for _, part := range strings.Split(style, ";") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		prop := strings.TrimSpace(strings.ToLower(kv[0]))
		val := strings.TrimSpace(strings.ToLower(kv[1]))
		switch prop {
		case "color":
			textColor = cssColorToDocxFontColor(val)
		case "background-color":
			bgColor = cssColorToDocxBgColor(val)
		}
	}
	return textColor, bgColor
}

// cssColorToDocxFontColor 将 CSS 颜色值（命名或十六进制）转为飞书字体颜色
func cssColorToDocxFontColor(color string) lark.DocxFontColor {
	color = strings.TrimSpace(strings.ToLower(color))
	if v, ok := cssNameToDocxFontColor[color]; ok {
		return v
	}
	if strings.HasPrefix(color, "#") {
		if hue, ok := hueFromHex(color); ok {
			return hueToDocxFontColor(hue)
		}
	}
	return 0
}

// cssColorToDocxBgColor 将 CSS 颜色值（命名或十六进制）转为飞书背景色
func cssColorToDocxBgColor(color string) lark.DocxFontBackgroundColor {
	color = strings.TrimSpace(strings.ToLower(color))
	if v, ok := cssNameToDocxBgColor[color]; ok {
		return v
	}
	if strings.HasPrefix(color, "#") {
		if hue, ok := hueFromHex(color); ok {
			return hueToDocxBgColor(hue)
		}
	}
	return 0
}

// hueFromHex 解析十六进制颜色并返回色相角度（0-359），灰色返回 -1
func hueFromHex(hex string) (int, bool) {
	hex = strings.TrimPrefix(hex, "#")
	switch len(hex) {
	case 3:
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
		// ok
	default:
		return 0, false
	}
	val, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, false
	}
	r, g, b := int(val>>16), int(val>>8&0xFF), int(val&0xFF)
	maxV := max(r, g, b)
	minV := min(r, g, b)
	delta := maxV - minV
	if delta == 0 {
		return -1, true // achromatic → grey
	}
	var hue int
	switch maxV {
	case r:
		hue = 60 * (g - b) / delta
	case g:
		hue = 60*(b-r)/delta + 120
	case b:
		hue = 60*(r-g)/delta + 240
	}
	if hue < 0 {
		hue += 360
	}
	return hue, true
}

// hueToDocxFontColor 根据色相角度映射到飞书字体颜色
func hueToDocxFontColor(hue int) lark.DocxFontColor {
	if hue < 0 {
		return lark.DocxFontColorLightGrey
	}
	switch {
	case hue < 10 || hue >= 340:
		return lark.DocxFontColorLightPink // red
	case hue < 45:
		return lark.DocxFontColorLightOrange
	case hue < 75:
		return lark.DocxFontColorLightYellow
	case hue < 165:
		return lark.DocxFontColorLightGreen
	case hue < 265:
		return lark.DocxFontColorLightBlue
	default:
		return lark.DocxFontColorLightPurple
	}
}

// hueToDocxBgColor 根据色相角度映射到飞书背景色
func hueToDocxBgColor(hue int) lark.DocxFontBackgroundColor {
	if hue < 0 {
		return lark.DocxFontBackgroundColorLightGrey
	}
	switch {
	case hue < 10 || hue >= 340:
		return lark.DocxFontBackgroundColorLightPink // red
	case hue < 45:
		return lark.DocxFontBackgroundColorLightOrange
	case hue < 75:
		return lark.DocxFontBackgroundColorLightYellow
	case hue < 165:
		return lark.DocxFontBackgroundColorLightGreen
	case hue < 265:
		return lark.DocxFontBackgroundColorLightBlue
	default:
		return lark.DocxFontBackgroundColorLightPurple
	}
}
