package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
)

const addOnsMermaidComponentTypeID = "blk_631fefbbae02400430b8f9f4"

type Parser struct {
	client      *Client
	useHTMLTags bool
	ImgTokens   []string
	FileTokens  []string
	RefDocs     []DocRef // 正文引用的其他 docx/wiki 文档（@文档 mention / 行内链接），--follow 消费
	refSeen     map[string]bool
	blockMap    map[string]*lark.DocxBlock
	Headings    []Heading         // from HEAD: 收集的标题（扁平列表）
	ctx         context.Context   // from 0182
	outputDir   string            // from 0182
	documentID  string            // 文档 ID
	prefixURL   string            // 域名前缀（如 https://feishu.cn）
	objEditTime string            // Wiki 节点编辑时间（用于白板缓存）
	userNames   map[string]string // MentionUser OpenID → 显示名
}

func NewParser(config OutputConfig, client *Client) *Parser {
	return &Parser{
		client:      client,
		useHTMLTags: config.UseHTMLTags,
		ImgTokens:   make([]string, 0),
		FileTokens:  make([]string, 0),
		refSeen:     make(map[string]bool),
		blockMap:    make(map[string]*lark.DocxBlock),
		Headings:    make([]Heading, 0),
		ctx:         context.Background(),
		outputDir:   "",
	}
}

// SetContext sets the context for the parser
func (p *Parser) SetContext(ctx context.Context) {
	p.ctx = ctx
}

// SetOutputDir sets the output directory for the parser
func (p *Parser) SetOutputDir(outputDir string) {
	p.outputDir = outputDir
}

// SetPrefixURL sets the domain prefix URL for the parser
func (p *Parser) SetPrefixURL(prefixURL string) {
	p.prefixURL = prefixURL
}

// SetObjEditTime sets the Wiki node edit time for whiteboard caching
func (p *Parser) SetObjEditTime(objEditTime string) {
	p.objEditTime = objEditTime
}

// SetUserNames sets the MentionUser OpenID → display name mapping
func (p *Parser) SetUserNames(m map[string]string) {
	p.userNames = m
}

// collectRef 旁路收集正文中的文档引用（按 token 去重），不影响渲染输出。
func (p *Parser) collectRef(ref DocRef, ok bool) {
	if !ok || p.refSeen[ref.Token] {
		return
	}
	p.refSeen[ref.Token] = true
	p.RefDocs = append(p.RefDocs, ref)
}

// =============================================================
// Parser utils
// =============================================================

var DocxCodeLang2MdStr = map[lark.DocxCodeLanguage]string{
	lark.DocxCodeLanguagePlainText:    "",
	lark.DocxCodeLanguageABAP:         "abap",
	lark.DocxCodeLanguageAda:          "ada",
	lark.DocxCodeLanguageApache:       "apache",
	lark.DocxCodeLanguageApex:         "apex",
	lark.DocxCodeLanguageAssembly:     "assembly",
	lark.DocxCodeLanguageBash:         "bash",
	lark.DocxCodeLanguageCSharp:       "csharp",
	lark.DocxCodeLanguageCPlusPlus:    "cpp",
	lark.DocxCodeLanguageC:            "c",
	lark.DocxCodeLanguageCOBOL:        "cobol",
	lark.DocxCodeLanguageCSS:          "css",
	lark.DocxCodeLanguageCoffeeScript: "coffeescript",
	lark.DocxCodeLanguageD:            "d",
	lark.DocxCodeLanguageDart:         "dart",
	lark.DocxCodeLanguageDelphi:       "delphi",
	lark.DocxCodeLanguageDjango:       "django",
	lark.DocxCodeLanguageDockerfile:   "dockerfile",
	lark.DocxCodeLanguageErlang:       "erlang",
	lark.DocxCodeLanguageFortran:      "fortran",
	lark.DocxCodeLanguageFoxPro:       "foxpro",
	lark.DocxCodeLanguageGo:           "go",
	lark.DocxCodeLanguageGroovy:       "groovy",
	lark.DocxCodeLanguageHTML:         "html",
	lark.DocxCodeLanguageHTMLBars:     "htmlbars",
	lark.DocxCodeLanguageHTTP:         "http",
	lark.DocxCodeLanguageHaskell:      "haskell",
	lark.DocxCodeLanguageJSON:         "json",
	lark.DocxCodeLanguageJava:         "java",
	lark.DocxCodeLanguageJavaScript:   "javascript",
	lark.DocxCodeLanguageJulia:        "julia",
	lark.DocxCodeLanguageKotlin:       "kotlin",
	lark.DocxCodeLanguageLateX:        "latex",
	lark.DocxCodeLanguageLisp:         "lisp",
	lark.DocxCodeLanguageLogo:         "logo",
	lark.DocxCodeLanguageLua:          "lua",
	lark.DocxCodeLanguageMATLAB:       "matlab",
	lark.DocxCodeLanguageMakefile:     "makefile",
	lark.DocxCodeLanguageMarkdown:     "markdown",
	lark.DocxCodeLanguageNginx:        "nginx",
	lark.DocxCodeLanguageObjective:    "objectivec",
	lark.DocxCodeLanguageOpenEdgeABL:  "openedge-abl",
	lark.DocxCodeLanguagePHP:          "php",
	lark.DocxCodeLanguagePerl:         "perl",
	lark.DocxCodeLanguagePostScript:   "postscript",
	lark.DocxCodeLanguagePower:        "powershell",
	lark.DocxCodeLanguageProlog:       "prolog",
	lark.DocxCodeLanguageProtoBuf:     "protobuf",
	lark.DocxCodeLanguagePython:       "python",
	lark.DocxCodeLanguageR:            "r",
	lark.DocxCodeLanguageRPG:          "rpg",
	lark.DocxCodeLanguageRuby:         "ruby",
	lark.DocxCodeLanguageRust:         "rust",
	lark.DocxCodeLanguageSAS:          "sas",
	lark.DocxCodeLanguageSCSS:         "scss",
	lark.DocxCodeLanguageSQL:          "sql",
	lark.DocxCodeLanguageScala:        "scala",
	lark.DocxCodeLanguageScheme:       "scheme",
	lark.DocxCodeLanguageScratch:      "scratch",
	lark.DocxCodeLanguageShell:        "shell",
	lark.DocxCodeLanguageSwift:        "swift",
	lark.DocxCodeLanguageThrift:       "thrift",
	lark.DocxCodeLanguageTypeScript:   "typescript",
	lark.DocxCodeLanguageVBScript:     "vbscript",
	lark.DocxCodeLanguageVisual:       "vbnet",
	lark.DocxCodeLanguageXML:          "xml",
	lark.DocxCodeLanguageYAML:         "yaml",
}

// mergeSpan 统一的合并单元格信息（行列跨度）
type mergeSpan struct {
	RowSpan int64
	ColSpan int64
}

// writeSimpleMarkdownTable 将 [][]string 渲染为简单的 pipe-delimited Markdown 表格
func writeSimpleMarkdownTable(buf *strings.Builder, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	buf.WriteString("|")
	for _, cell := range rows[0] {
		buf.WriteString(" " + cell + " |")
	}
	buf.WriteString("\n|")
	for range rows[0] {
		buf.WriteString(" --- |")
	}
	buf.WriteString("\n")
	for i := 1; i < len(rows); i++ {
		buf.WriteString("|")
		for _, cell := range rows[i] {
			buf.WriteString(" " + cell + " |")
		}
		buf.WriteString("\n")
	}
}

// writeHTMLTable 渲染带合并单元格的 HTML 表格
// mergeMap: mergeMap[row][col] = &mergeSpan{RowSpan, ColSpan}
func writeHTMLTable(buf *strings.Builder, rows [][]string, mergeMap map[int64]map[int64]*mergeSpan) {
	buf.WriteString("<table>\n")
	processedCells := map[string]bool{}
	for rowIndex, row := range rows {
		buf.WriteString("<tr>\n")
		for colIndex, cellContent := range row {
			cellKey := fmt.Sprintf("%d-%d", rowIndex, colIndex)
			if processedCells[cellKey] {
				continue
			}
			var ms *mergeSpan
			if rowMap, ok := mergeMap[int64(rowIndex)]; ok {
				ms = rowMap[int64(colIndex)]
			}
			if ms != nil && (ms.RowSpan > 1 || ms.ColSpan > 1) {
				attrs := ""
				if ms.RowSpan > 1 {
					attrs += fmt.Sprintf(` rowspan="%d"`, ms.RowSpan)
				}
				if ms.ColSpan > 1 {
					attrs += fmt.Sprintf(` colspan="%d"`, ms.ColSpan)
				}
				buf.WriteString(fmt.Sprintf("<td%s>%s</td>\n", attrs, cellContent))
				for r := int64(rowIndex); r < int64(rowIndex)+ms.RowSpan; r++ {
					for c := int64(colIndex); c < int64(colIndex)+ms.ColSpan; c++ {
						processedCells[fmt.Sprintf("%d-%d", r, c)] = true
					}
				}
			} else {
				buf.WriteString(fmt.Sprintf("<td>%s</td>\n", cellContent))
			}
		}
		buf.WriteString("</tr>\n")
	}
	buf.WriteString("</table>\n")
}

// =============================================================
// Parse the new version of document (docx)
// =============================================================

func (p *Parser) ParseDocxContent(doc *lark.DocxDocument, blocks []*lark.DocxBlock) string {
	p.documentID = doc.DocumentID

	for _, block := range blocks {
		p.blockMap[block.BlockID] = block
	}

	entryBlock := p.blockMap[doc.DocumentID]
	if entryBlock == nil {
		return ""
	}
	return p.ParseDocxBlock(entryBlock, 0)
}

func (p *Parser) ParseDocxBlock(b *lark.DocxBlock, indentLevel int) string {
	buf := new(strings.Builder)
	buf.WriteString(strings.Repeat("\t", indentLevel))

	switch b.BlockType {
	case lark.DocxBlockTypePage:
		buf.WriteString(p.ParseDocxBlockPage(b))
	case lark.DocxBlockTypeText:
		if b.Text != nil && b.Text.Style != nil && b.Text.Style.Folded && len(b.Children) > 0 {
			buf.WriteString(p.ParseDocxBlockFolded(b))
		} else {
			buf.WriteString(p.ParseDocxBlockText(b.Text))
		}
	case lark.DocxBlockTypeCallout:
		buf.WriteString(p.ParseDocxBlockCallout(b))
	case lark.DocxBlockTypeHeading1:
		buf.WriteString(p.ParseDocxBlockHeading(b, 1))
	case lark.DocxBlockTypeHeading2:
		buf.WriteString(p.ParseDocxBlockHeading(b, 2))
	case lark.DocxBlockTypeHeading3:
		buf.WriteString(p.ParseDocxBlockHeading(b, 3))
	case lark.DocxBlockTypeHeading4:
		buf.WriteString(p.ParseDocxBlockHeading(b, 4))
	case lark.DocxBlockTypeHeading5:
		buf.WriteString(p.ParseDocxBlockHeading(b, 5))
	case lark.DocxBlockTypeHeading6:
		buf.WriteString(p.ParseDocxBlockHeading(b, 6))
	case lark.DocxBlockTypeHeading7:
		buf.WriteString(p.ParseDocxBlockHeading(b, 7))
	case lark.DocxBlockTypeHeading8:
		buf.WriteString(p.ParseDocxBlockHeading(b, 8))
	case lark.DocxBlockTypeHeading9:
		buf.WriteString(p.ParseDocxBlockHeading(b, 9))
	case lark.DocxBlockTypeBullet:
		buf.WriteString(p.ParseDocxBlockBullet(b, indentLevel))
	case lark.DocxBlockTypeOrdered:
		buf.WriteString(p.ParseDocxBlockOrdered(b, indentLevel))
	case lark.DocxBlockTypeCode:
		buf.WriteString("```" + DocxCodeLang2MdStr[b.Code.Style.Language] + "\n")
		buf.WriteString(strings.TrimSpace(p.ParseDocxBlockText(b.Code)))
		buf.WriteString("\n```\n")
	case lark.DocxBlockTypeQuote:
		buf.WriteString("> ")
		buf.WriteString(p.ParseDocxBlockText(b.Quote))
	case lark.DocxBlockTypeEquation:
		buf.WriteString("$$\n")
		buf.WriteString(p.ParseDocxBlockText(b.Equation))
		buf.WriteString("\n$$\n")
	case lark.DocxBlockTypeTodo:
		if b.Todo.Style.Done {
			buf.WriteString("- [x] ")
		} else {
			buf.WriteString("- [ ] ")
		}
		buf.WriteString(p.ParseDocxBlockText(b.Todo))
		buf.WriteString(p.renderListItemChildren(b))
	case lark.DocxBlockTypeDivider:
		buf.WriteString("---\n")
	case lark.DocxBlockTypeImage:
		buf.WriteString(p.ParseDocxBlockImage(b.Image))
	case lark.DocxBlockTypeFile:
		buf.WriteString(p.ParseDocxBlockFile(b.File))
	case lark.DocxBlockTypeBitable:
		buf.WriteString(p.ParseDocxBlockBitable(b.Bitable))
	case lark.DocxBlockTypeDiagram:
		buf.WriteString(p.ParseDocxBlockDiagram(b.Diagram))
	case lark.DocxBlockTypeIframe:
		buf.WriteString(p.ParseDocxBlockIframe(b.Iframe))
	case lark.DocxBlockTypeTableCell:
		buf.WriteString(p.ParseDocxBlockTableCell(b))
	case lark.DocxBlockTypeTable:
		buf.WriteString(p.ParseDocxBlockTable(b.Table))
	case lark.DocxBlockTypeSheet:
		buf.WriteString(p.ParseDocxBlockSheet(b.Sheet))
	case lark.DocxBlockTypeQuoteContainer:
		buf.WriteString(p.ParseDocxBlockQuoteContainer(b))
	case lark.DocxBlockTypeGrid:
		buf.WriteString(p.ParseDocxBlockGrid(b, indentLevel))
	case lark.DocxBlockTypeBoard:
		buf.WriteString(p.ParseDocxBlockBoard(b.Board, b.BlockID))
	case lark.DocxBlockTypeAddOns:
		buf.WriteString(p.ParseDocxBlockAddOns(b.AddOns))
	default:
		// 对于不支持的 block type，仍然处理其 children
		for _, childId := range b.Children {
			childBlock := p.blockMap[childId]
			buf.WriteString(p.ParseDocxBlock(childBlock, indentLevel))
		}
	}
	return buf.String()
}

func (p *Parser) ParseDocxBlockPage(b *lark.DocxBlock) string {
	buf := new(strings.Builder)

	buf.WriteString("# ")
	buf.WriteString(p.ParseDocxBlockText(b.Page))
	buf.WriteString("\n")

	for i, childId := range b.Children {
		childBlock := p.blockMap[childId]
		buf.WriteString(p.ParseDocxBlock(childBlock, 0))

		if i == len(b.Children)-1 {
			continue
		}
		next := p.blockMap[b.Children[i+1]]
		// 相邻同族 list 块之间用 tight 分隔（仅块自身末尾的 \n），
		// 让下载产物粘贴回飞书 Wiki 时不产生幽灵空段落。
		if isSameFamilyList(childBlock, next) {
			continue
		}
		buf.WriteString("\n")
	}

	result := buf.String()
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

func isSameFamilyList(a, b *lark.DocxBlock) bool {
	if a == nil || b == nil {
		return false
	}
	switch a.BlockType {
	case lark.DocxBlockTypeBullet, lark.DocxBlockTypeOrdered, lark.DocxBlockTypeTodo:
		return a.BlockType == b.BlockType
	}
	return false
}

func (p *Parser) ParseDocxBlockFolded(b *lark.DocxBlock) string {
	buf := new(strings.Builder)
	buf.WriteString("<details>\n<summary>")
	text := strings.TrimRight(p.ParseDocxBlockText(b.Text), "\n")
	buf.WriteString(text)
	buf.WriteString("</summary>\n\n")
	for _, childId := range b.Children {
		childBlock := p.blockMap[childId]
		buf.WriteString(p.ParseDocxBlock(childBlock, 0))
	}
	buf.WriteString("\n</details>\n")
	return buf.String()
}

func (p *Parser) ParseDocxBlockText(b *lark.DocxBlockText) string {
	buf := new(strings.Builder)
	numElem := len(b.Elements)
	for _, e := range b.Elements {
		inline := numElem > 1
		buf.WriteString(p.ParseDocxTextElement(e, inline))
	}
	buf.WriteString("\n")
	return buf.String()
}

// calloutColorToType 将飞书 callout 背景色映射到 GitHub Alerts 类型
var calloutColorToType = map[lark.DocxCalloutBackgroundColor]string{
	lark.DocxCalloutBackgroundColorLightRed:    "CAUTION",   // 红 → 危险
	lark.DocxCalloutBackgroundColorDarkRed:     "CAUTION",   // 红 → 危险
	lark.DocxCalloutBackgroundColorLightOrange: "WARNING",   // 橙 → 警告
	lark.DocxCalloutBackgroundColorDarkOrange:  "WARNING",   // 橙 → 警告
	lark.DocxCalloutBackgroundColorLightYellow: "WARNING",   // 黄 → 警告
	lark.DocxCalloutBackgroundColorDarkYellow:  "WARNING",   // 黄 → 警告
	lark.DocxCalloutBackgroundColorLightGreen:  "TIP",       // 绿 → 建议
	lark.DocxCalloutBackgroundColorDarkGreen:   "TIP",       // 绿 → 建议
	lark.DocxCalloutBackgroundColorLightBlue:   "NOTE",      // 蓝 → 信息
	lark.DocxCalloutBackgroundColorDarkBlue:    "NOTE",      // 蓝 → 信息
	lark.DocxCalloutBackgroundColorLightPurple: "IMPORTANT", // 紫 → 重要
	lark.DocxCalloutBackgroundColorDarkPurple:  "IMPORTANT", // 紫 → 重要
	lark.DocxCalloutBackgroundColorLightGrey:   "NOTE",      // 灰 → 信息
	lark.DocxCalloutBackgroundColorDarkGrey:    "NOTE",      // 灰 → 信息
}

func (p *Parser) ParseDocxBlockCallout(b *lark.DocxBlock) string {
	buf := new(strings.Builder)

	// 根据背景色判断类型，默认 TIP
	alertType := "TIP"
	if b.Callout != nil {
		if t, ok := calloutColorToType[b.Callout.BackgroundColor]; ok {
			alertType = t
		}
	}

	buf.WriteString("> [!")
	buf.WriteString(alertType)
	buf.WriteString("]\n")

	for i, childId := range b.Children {
		childBlock := p.blockMap[childId]
		content := p.ParseDocxBlock(childBlock, 0)
		content = strings.TrimRight(content, "\n")
		// 逐行添加 > 前缀
		lines := strings.Split(content, "\n")
		for j, line := range lines {
			buf.WriteString("> ")
			buf.WriteString(line)
			if j < len(lines)-1 {
				buf.WriteString("\n")
			}
		}
		if i < len(b.Children)-1 {
			buf.WriteString("\n")
		}
	}
	buf.WriteString("\n")
	return buf.String()
}
func (p *Parser) ParseDocxTextElement(e *lark.DocxTextElement, inline bool) string {
	buf := new(strings.Builder)
	if e.TextRun != nil {
		buf.WriteString(p.ParseDocxTextElementTextRun(e.TextRun))
	}
	if e.MentionUser != nil {
		buf.WriteString(p.renderMentionUser(e.MentionUser))
	}
	if e.MentionDoc != nil {
		buf.WriteString(renderMentionDoc(e.MentionDoc))
		p.collectRef(DocRefFromMention(e.MentionDoc.Token,
			mentionObjTypeString(e.MentionDoc.ObjType),
			utils.UnescapeURL(e.MentionDoc.URL), e.MentionDoc.Title))
	}
	if e.Equation != nil {
		symbol := "$$"
		if inline {
			symbol = "$"
		}
		buf.WriteString(symbol + strings.TrimSuffix(e.Equation.Content, "\n") + symbol)
	}
	return buf.String()
}

func (p *Parser) ParseDocxTextElementTextRun(tr *lark.DocxTextElementTextRun) string {
	buf := new(strings.Builder)
	var openers, closers []string
	isInlineCode := tr.TextElementStyle != nil && tr.TextElementStyle.InlineCode

	if style := tr.TextElementStyle; style != nil {
		// InlineCode 优先级最高：内部不再叠加其他 Markdown 样式
		if style.InlineCode {
			openers = append(openers, "`")
			closers = append(closers, "`")
		} else {
			if link := style.Link; link != nil {
				openers = append(openers, "[")
				closers = append(closers, fmt.Sprintf("](%s)", utils.UnescapeURL(link.URL)))
				p.collectRef(DocRefFromLink(utils.UnescapeURL(link.URL), tr.Content))
			}
			if style.Bold {
				if p.useHTMLTags {
					openers = append(openers, "<strong>")
					closers = append(closers, "</strong>")
				} else {
					openers = append(openers, "**")
					closers = append(closers, "**")
				}
			}
			if style.Italic {
				if p.useHTMLTags {
					openers = append(openers, "<em>")
					closers = append(closers, "</em>")
				} else {
					// 用 * 而非 _：CommonMark 的 _ 强调不支持 CJK/intraword
					// （`_斜体_` 不会被解析为斜体），* 对中英文都成立。
					openers = append(openers, "*")
					closers = append(closers, "*")
				}
			}
			if style.Strikethrough {
				if p.useHTMLTags {
					openers = append(openers, "<del>")
					closers = append(closers, "</del>")
				} else {
					openers = append(openers, "~~")
					closers = append(closers, "~~")
				}
			}
			if style.Underline {
				openers = append(openers, "<u>")
				closers = append(closers, "</u>")
			}
		}
	}

	content := tr.Content
	lead, trail := "", ""
	// 把 emphasis 标记两端的空白外提：CommonMark 规定 `**x **` 这类结束符紧贴空白时
	// 不构成强调，会被当作字面星号，导致上传丢样式且签名不匹配。InlineCode 不外提
	// （代码内空白有语义，且飞书侧也保留在内）。
	if len(openers) > 0 && !isInlineCode {
		t := strings.TrimLeft(content, " \t")
		lead = content[:len(content)-len(t)]
		core := strings.TrimRight(t, " \t")
		trail = t[len(core):]
		if core == "" {
			// 全为空白：不施加任何标记，原样输出
			openers, closers, lead, trail = nil, nil, "", ""
		} else {
			content = core
		}
	}

	buf.WriteString(lead)
	for _, o := range openers {
		buf.WriteString(o)
	}
	buf.WriteString(content)
	// 逆序关闭标记，保证正确嵌套
	for i := len(closers) - 1; i >= 0; i-- {
		buf.WriteString(closers[i])
	}
	buf.WriteString(trail)

	return buf.String()
}

func (p *Parser) ParseDocxBlockHeading(b *lark.DocxBlock, headingLevel int) string {
	buf := new(strings.Builder)

	buf.WriteString(strings.Repeat("#", headingLevel))
	buf.WriteString(" ")

	blockText := getBlockText(b)

	// 收集标题纯文本
	plainText := p.extractHeadingPlainText(blockText)
	p.Headings = append(p.Headings, Heading{Level: headingLevel, Text: plainText})

	buf.WriteString(p.ParseDocxBlockText(blockText))

	for _, childId := range b.Children {
		childBlock := p.blockMap[childId]
		buf.WriteString(p.ParseDocxBlock(childBlock, 0))
	}

	return buf.String()
}

// extractHeadingPlainText 提取标题纯文本（不含 Markdown 格式）
func (p *Parser) extractHeadingPlainText(blockText *lark.DocxBlockText) string {
	var buf strings.Builder
	for _, e := range blockText.Elements {
		if e.TextRun != nil {
			buf.WriteString(e.TextRun.Content)
		}
	}
	return strings.TrimSpace(buf.String())
}

// GetHeadings 返回收集的标题列表
func (p *Parser) GetHeadings() []Heading {
	return p.Headings
}

func (p *Parser) ParseDocxBlockImage(img *lark.DocxBlockImage) string {
	buf := new(strings.Builder)
	buf.WriteString(fmt.Sprintf("![](%s)", img.Token))
	buf.WriteString("\n")
	p.ImgTokens = append(p.ImgTokens, img.Token)
	return buf.String()
}

func (p *Parser) ParseDocxBlockFile(file *lark.DocxBlockFile) string {
	fileName := file.Name
	if fileName == "" {
		fileName = file.Token
	}
	p.FileTokens = append(p.FileTokens, file.Token)
	return fmt.Sprintf("[%s](%s)\n", fileName, file.Token)
}

// isListChildType 判断块类型是否为列表项（bullet/ordered/todo）。
func isListChildType(bt lark.DocxBlockType) bool {
	switch bt {
	case lark.DocxBlockTypeBullet, lark.DocxBlockTypeOrdered, lark.DocxBlockTypeTodo:
		return true
	}
	return false
}

// noBlankLineNeeded 判断列表项子块是否「自带块边界」、紧跟父项也能被正确解析为独立块
// （子列表的 marker、代码块的围栏）。这类块保持 tight 输出（与网页粘贴排版一致）；
// 其余块（段落/分隔线/图片等）需空行分隔，否则会被合并进本体段落或误解析
// （如 --- 紧跟段落变 setext 标题、图片变 inline）。
func noBlankLineNeeded(bt lark.DocxBlockType) bool {
	return isListChildType(bt) || bt == lark.DocxBlockTypeCode
}

// indentBlockLines 给字符串的每个非空行前加 indent 前缀（空行保持，避免 trailing whitespace）。
func indentBlockLines(s, indent string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, "\n")
}

// renderListItemChildren 渲染列表项的子块并缩进一级：子列表 tight 紧跟，其余块
// （段落/代码/分隔线/图片等）前置空行分隔，保证 round-trip 时被解析为该列表项的 child
// 而非合并进本体段落。
func (p *Parser) renderListItemChildren(b *lark.DocxBlock) string {
	var buf strings.Builder
	for _, childId := range b.Children {
		childBlock := p.blockMap[childId]
		child := p.ParseDocxBlock(childBlock, 0)
		if !noBlankLineNeeded(childBlock.BlockType) {
			buf.WriteString("\n")
		}
		buf.WriteString(indentBlockLines(child, "\t"))
	}
	return buf.String()
}

func (p *Parser) ParseDocxBlockBullet(b *lark.DocxBlock, indentLevel int) string {
	buf := new(strings.Builder)

	buf.WriteString("- ")
	buf.WriteString(p.ParseDocxBlockText(b.Bullet))
	buf.WriteString(p.renderListItemChildren(b))

	return buf.String()
}

func (p *Parser) ParseDocxBlockOrdered(b *lark.DocxBlock, indentLevel int) string {
	buf := new(strings.Builder)

	// calculate order and indent level
	parent := p.blockMap[b.ParentID]
	order := 1
	for idx, child := range parent.Children {
		if child == b.BlockID {
			for i := idx - 1; i >= 0; i-- {
				if p.blockMap[parent.Children[i]].BlockType == lark.DocxBlockTypeOrdered {
					order += 1
				} else {
					break
				}
			}
			break
		}
	}

	buf.WriteString(fmt.Sprintf("%d. ", order))
	buf.WriteString(p.ParseDocxBlockText(b.Ordered))
	buf.WriteString(p.renderListItemChildren(b))

	return buf.String()
}

func (p *Parser) ParseDocxBlockTableCell(b *lark.DocxBlock) string {
	buf := new(strings.Builder)

	for _, child := range b.Children {
		block := p.blockMap[child]
		content := p.ParseDocxBlock(block, 0)
		buf.WriteString(content + "<br/>")
	}

	return buf.String()
}

func (p *Parser) ParseDocxBlockTable(t *lark.DocxBlockTable) string {
	var rows [][]string
	mergeInfoMap := map[int64]map[int64]*lark.DocxBlockTablePropertyMergeInfo{}

	// 构建单元格合并信息的映射，并检测是否有实际的合并单元格
	hasMerge := false
	if t.Property.MergeInfo != nil {
		for i, merge := range t.Property.MergeInfo {
			rowIndex := int64(i) / t.Property.ColumnSize
			colIndex := int64(i) % t.Property.ColumnSize
			if _, exists := mergeInfoMap[int64(rowIndex)]; !exists {
				mergeInfoMap[int64(rowIndex)] = map[int64]*lark.DocxBlockTablePropertyMergeInfo{}
			}
			mergeInfoMap[rowIndex][colIndex] = merge
			// 检测是否有实际的合并（RowSpan > 1 或 ColSpan > 1）
			if merge.RowSpan > 1 || merge.ColSpan > 1 {
				hasMerge = true
			}
		}
	}

	// 构建表格内容
	for i, blockId := range t.Cells {
		block := p.blockMap[blockId]
		cellContent := p.ParseDocxBlock(block, 0)
		cellContent = strings.ReplaceAll(cellContent, "\n", "")
		rowIndex := int64(i) / t.Property.ColumnSize
		colIndex := int64(i) % t.Property.ColumnSize

		// 初始化行
		for len(rows) <= int(rowIndex) {
			rows = append(rows, []string{})
		}
		for len(rows[rowIndex]) <= int(colIndex) {
			rows[rowIndex] = append(rows[rowIndex], "")
		}
		// 设置单元格内容
		rows[rowIndex][colIndex] = cellContent
	}

	buf := new(strings.Builder)

	if hasMerge {
		// 转换 mergeInfoMap 到通用 mergeSpan
		spanMap := make(map[int64]map[int64]*mergeSpan)
		for r, cols := range mergeInfoMap {
			spanMap[r] = make(map[int64]*mergeSpan)
			for c, mi := range cols {
				spanMap[r][c] = &mergeSpan{RowSpan: mi.RowSpan, ColSpan: mi.ColSpan}
			}
		}
		writeHTMLTable(buf, rows, spanMap)
	} else if len(rows) > 0 {
		// 无合并：去除 <br/> 后缀再渲染 Markdown 表格
		cleaned := make([][]string, len(rows))
		for i, row := range rows {
			cleaned[i] = make([]string, len(row))
			for j, cell := range row {
				cleaned[i][j] = strings.TrimSuffix(cell, "<br/>")
			}
		}
		writeSimpleMarkdownTable(buf, cleaned)
	}

	return buf.String()
}

func (p *Parser) ParseDocxBlockQuoteContainer(b *lark.DocxBlock) string {
	buf := new(strings.Builder)

	for i, child := range b.Children {
		block := p.blockMap[child]
		content := p.ParseDocxBlock(block, 0)
		// 移除内容末尾的换行符
		content = strings.TrimRight(content, "\n")
		// 给子块内容的每一行都加 `>` 前缀：否则多行子块（如代码块）只有首行在引用内，
		// 其余行逃逸到引用外，导致代码围栏不闭合、吞掉后续块（round-trip 结构错乱）。
		for j, line := range strings.Split(content, "\n") {
			if j > 0 {
				buf.WriteString("\n")
			}
			if line == "" {
				buf.WriteString(">")
			} else {
				buf.WriteString("> ")
				buf.WriteString(line)
			}
		}
		// 子段落之间插入空引用行（> ），否则相邻 `> ` 行会被 CommonMark 合并成
		// 单个段落，导致上传重建为单 Text 子块、与远程多 Text 子块结构不一致（round-trip 抖动）。
		if i < len(b.Children)-1 {
			buf.WriteString("\n>\n")
		}
	}
	buf.WriteString("\n")

	return buf.String()
}

func (p *Parser) ParseDocxBlockGrid(b *lark.DocxBlock, indentLevel int) string {
	buf := new(strings.Builder)

	var blocks []*lark.DocxBlock
	for _, child := range b.Children {
		columnBlock := p.blockMap[child]
		for _, gchild := range columnBlock.Children {
			blocks = append(blocks, p.blockMap[gchild])
		}
	}

	for i, block := range blocks {
		buf.WriteString(p.ParseDocxBlock(block, indentLevel))
		if i == len(blocks)-1 {
			continue
		}
		if isSameFamilyList(block, blocks[i+1]) {
			continue
		}
		buf.WriteString("\n")
	}

	return buf.String()
}

func (p *Parser) ParseDocxBlockSheet(s *lark.DocxBlockSheet) string {
	// 电子表格块（Sheet）是嵌入到飞书文档中的外部电子表格
	buf := new(strings.Builder)

	// 如果没有 client 或 token，则返回占位符
	if p.client == nil || s.Token == "" {
		buf.WriteString("> **📊 嵌入的电子表格**\n")
		buf.WriteString(">\n")
		if s.Token != "" {
			buf.WriteString(fmt.Sprintf("> Token: `%s`\n", s.Token))
		}
		buf.WriteString(">\n")
		buf.WriteString("> *注：无法获取电子表格内容（缺少 client 或 token）*\n")
		return buf.String()
	}

	// 尝试获取电子表格的实际内容
	ctx := context.Background()
	sheetData, err := p.client.GetSheetContent(ctx, s.Token)
	if err != nil {
		// 如果获取失败，返回占位符
		buf.WriteString("> **📊 嵌入的电子表格**\n")
		buf.WriteString(">\n")
		if s.Token != "" {
			buf.WriteString(fmt.Sprintf("> Token: `%s`\n", s.Token))
		}
		buf.WriteString(">\n")
		// 检查是否是 token 格式问题
		if strings.Contains(err.Error(), "invalid spreadsheet token format") {
			buf.WriteString("> *注：此电子表格使用了不支持的嵌入方式，无法获取内容*\n")
		} else if strings.Contains(err.Error(), "91402") || strings.Contains(err.Error(), "NOTEXIST") {
			buf.WriteString("> *注：无法访问电子表格（可能没有权限或电子表格不存在）*\n")
		} else {
			buf.WriteString(fmt.Sprintf("> *获取电子表格内容失败: %v*\n", err))
		}
		return buf.String()
	}

	// 将电子表格数据转换为 HTML 表格
	if len(sheetData.Values) == 0 {
		buf.WriteString("> **📊 嵌入的电子表格**\n")
		buf.WriteString(">\n")
		if s.Token != "" {
			buf.WriteString(fmt.Sprintf("> Token: `%s`\n", s.Token))
		}
		buf.WriteString(">\n")
		buf.WriteString("> *电子表格为空*\n")
		return buf.String()
	}

	// 根据是否有合并单元格选择输出格式
	if len(sheetData.Merges) > 0 {
		spanMap := make(map[int64]map[int64]*mergeSpan)
		for _, merge := range sheetData.Merges {
			if _, exists := spanMap[merge.StartRowIndex]; !exists {
				spanMap[merge.StartRowIndex] = make(map[int64]*mergeSpan)
			}
			spanMap[merge.StartRowIndex][merge.StartColumnIndex] = &mergeSpan{
				RowSpan: merge.RowSpan,
				ColSpan: merge.ColSpan,
			}
		}
		writeHTMLTable(buf, sheetData.Values, spanMap)
	} else {
		writeSimpleMarkdownTable(buf, sheetData.Values)
	}

	return buf.String()
}

// ParseDocxBlockBitable 解析多维表格块
func (p *Parser) ParseDocxBlockBitable(bitable *lark.DocxBlockBitable) string {
	buf := new(strings.Builder)

	// 如果没有 client 或 token，则返回占位符
	if p.client == nil || bitable.Token == "" {
		buf.WriteString("> **📊 多维表格**\n")
		buf.WriteString(">\n")
		if bitable.Token != "" {
			buf.WriteString(fmt.Sprintf("> Token: `%s`\n", bitable.Token))
		}
		buf.WriteString(">\n")
		buf.WriteString("> *注：无法获取多维表格内容（缺少 client 或 token）*\n")
		return buf.String()
	}

	// 尝试获取多维表格的实际内容
	ctx := context.Background()
	values, err := p.client.GetBitableContent(ctx, bitable.Token)
	if err != nil {
		// 如果获取失败，返回占位符
		buf.WriteString("> **📊 多维表格**\n")
		buf.WriteString(">\n")
		if bitable.Token != "" {
			buf.WriteString(fmt.Sprintf("> Token: `%s`\n", bitable.Token))
		}
		buf.WriteString(">\n")
		buf.WriteString(fmt.Sprintf("> *获取多维表格内容失败: %v*\n", err))
		return buf.String()
	}

	// 将多维表格数据转换为 markdown 表格
	if len(values) == 0 {
		buf.WriteString("> **📊 多维表格**\n")
		buf.WriteString(">\n")
		if bitable.Token != "" {
			buf.WriteString(fmt.Sprintf("> Token: `%s`\n", bitable.Token))
		}
		buf.WriteString(">\n")
		buf.WriteString("> *多维表格为空*\n")
		return buf.String()
	}

	// 生成 markdown 表格
	writeSimpleMarkdownTable(buf, values)

	return buf.String()
}

// ParseDocxBlockDiagram 解析流程图/UML块
func (p *Parser) ParseDocxBlockDiagram(diagram *lark.DocxBlockDiagram) string {
	buf := new(strings.Builder)

	const diagramTypeUML = 2
	diagramType := "流程图"
	if diagram.DiagramType == diagramTypeUML {
		diagramType = "UML图"
	}

	buf.WriteString(fmt.Sprintf("**📈 %s**\n\n", diagramType))
	buf.WriteString("> *注：流程图/UML图无法直接转换为 Markdown，建议导出为图片或使用 Mermaid 语法*\n")

	return buf.String()
}

// ParseDocxBlockBoard 解析画板块
func (p *Parser) ParseDocxBlockBoard(board *lark.DocxBlockBoard, blockID string) string {
	if board == nil || board.Token == "" {
		return ""
	}

	buf := new(strings.Builder)

	// 下载白板图片（仅作为下载缩略图保留；上传时跳过不传）
	var imgPath string
	if p.client != nil && p.outputDir != "" {
		var err error

		// 仅 Wiki 文档使用缓存（objEditTime 非空表示是 Wiki 文档）
		if p.objEditTime != "" {
			if cachePath, filename := p.client.imageCache.GetWhiteboard(board.Token, p.objEditTime); cachePath != "" {
				// 缓存命中，复制到输出目录
				destPath := filepath.Join(p.outputDir, filename)
				if copyErr := p.client.imageCache.CopyTo(cachePath, destPath); copyErr == nil {
					imgPath = destPath
					log.Printf("[whiteboard] 缓存命中: %s", board.Token)
				}
			}
		}

		// 缓存未命中或非 Wiki 文档，下载图片
		if imgPath == "" {
			var data []byte
			var filename string
			imgPath, data, filename, err = p.client.DownloadWhiteboardImage(p.ctx, board.Token, p.outputDir)
			if err != nil {
				log.Printf("警告: 白板图片下载失败 %s: %v", board.Token, err)
			} else if p.objEditTime != "" && len(data) > 0 {
				// Wiki 文档：写入缓存（保存裁剪后的版本）
				_ = p.client.imageCache.PutWhiteboard(board.Token, data, filename, p.objEditTime)
			}
		}
	}

	// round-trip 白板标记 <whiteboard>：
	//   token  — 白板身份，上传时复用原白板（不删除重建）
	//   href   — "查看原始画板"跳转链接
	//   width/height — 几何信息（上传忽略）
	//   内层 <img> — 下载缩略图，上传时跳过不传
	// 开标签必须独占一行，才会被 goldmark 判为 HTMLBlock（CommonMark type 7），
	// 上传侧据此走块级 HTML DOM 解析识别 token。
	var boardURL string
	if p.prefixURL != "" && p.documentID != "" {
		boardURL = fmt.Sprintf("%s/docx/%s?openbrd=1&doc_app_id=501&blockId=%s&blockType=whiteboard&blockToken=%s#%s",
			p.prefixURL, p.documentID, blockID, board.Token, blockID)
	} else {
		// 降级：使用旧的简单链接格式
		boardURL = fmt.Sprintf("%s/board/%s", p.prefixURL, board.Token)
	}

	attrs := "token=\"" + htmlAttrEscape(board.Token) + "\" href=\"" + htmlAttrEscape(boardURL) + "\""
	if board.Width > 0 {
		attrs += fmt.Sprintf(" width=\"%d\"", board.Width)
	}
	if board.Height > 0 {
		attrs += fmt.Sprintf(" height=\"%d\"", board.Height)
	}
	buf.WriteString("<whiteboard " + attrs + ">\n")
	if imgPath != "" {
		imgRel := filepath.Join(filepath.Base(p.outputDir), filepath.Base(imgPath))
		buf.WriteString("<img src=\"" + htmlAttrEscape(imgRel) + "\" alt=\"画板\"/>\n")
	}
	buf.WriteString("</whiteboard>\n")

	return buf.String()
}

// ParseDocxBlockAddOns 解析文档小组件块
func (p *Parser) ParseDocxBlockAddOns(addOns *lark.DocxBlockAddOns) string {
	if addOns == nil || addOns.Record == "" {
		return ""
	}

	var record struct {
		Data  string `json:"data"`
		Theme string `json:"theme"`
		View  string `json:"view"`
	}
	if err := json.Unmarshal([]byte(addOns.Record), &record); err != nil {
		return ""
	}

	switch addOns.ComponentTypeID {
	case addOnsMermaidComponentTypeID:
		return p.renderMermaidAddOn(record.Data)
	default:
		if record.Data != "" {
			return p.renderMermaidAddOn(record.Data)
		}
		return ""
	}
}

func (p *Parser) renderMermaidAddOn(data string) string {
	if data == "" {
		return ""
	}
	buf := new(strings.Builder)
	buf.WriteString("```mermaid\n")
	buf.WriteString(data)
	if !strings.HasSuffix(data, "\n") {
		buf.WriteString("\n")
	}
	buf.WriteString("```\n")
	return buf.String()
}

// CollectMentionUserIDs 从 blocks 中收集所有 MentionUser 的 UserID
func CollectMentionUserIDs(blocks []*lark.DocxBlock) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, b := range blocks {
		bt := getBlockText(b)
		if bt == nil {
			continue
		}
		for _, elem := range bt.Elements {
			if elem.MentionUser != nil && !seen[elem.MentionUser.UserID] {
				seen[elem.MentionUser.UserID] = true
				ids = append(ids, elem.MentionUser.UserID)
			}
		}
	}
	return ids
}

// ParseDocxBlockIframe 解析内嵌块
func (p *Parser) ParseDocxBlockIframe(iframe *lark.DocxBlockIframe) string {
	buf := new(strings.Builder)

	buf.WriteString("**🔗 嵌入内容**\n\n")

	if iframe.Component != nil {
		// 获取 iframe 类型名称
		typeNames := map[int]string{
			1:  "哔哩哔哩",
			2:  "西瓜视频",
			3:  "优酷",
			4:  "Airtable",
			5:  "百度地图",
			6:  "高德地图",
			7:  "TikTok",
			8:  "Figma",
			9:  "墨刀",
			10: "Canva",
			11: "CodePen",
			12: "飞书问卷",
			13: "金数据",
			14: "谷歌地图",
			15: "YouTube",
			99: "其他",
		}

		typeName := "未知类型"
		if name, ok := typeNames[int(iframe.Component.IframeType)]; ok {
			typeName = name
		}

		buf.WriteString(fmt.Sprintf("> 类型: %s\n", typeName))

		// 显示 URL（如果有的话）
		if iframe.Component.URL != "" {
			buf.WriteString(">\n")
			buf.WriteString(fmt.Sprintf("> 链接: %s\n", iframe.Component.URL))
		}
	}

	buf.WriteString(">\n")
	buf.WriteString("> *注：嵌入内容无法直接在 Markdown 中显示，请访问飞书查看原始内容*\n")

	return buf.String()
}
