package core

import (
	"fmt"
	"strings"

	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
	"github.com/yuin/goldmark/ast"
	"golang.org/x/net/html"
)

// 飞书原生「引用」(@文档/@人) 的扩展语法协议（v1）。
//
// 下载侧 parser 把 MentionUser/MentionDoc 产出为 lark-doc-xml.md 风格的 <cite> 标签；
// 上传侧 md2blocks 识别 <cite> 还原为对应的 DocxTextElement。
// 标签内文本为显示名/标题（可读 + 降级兜底），真实语义由属性承载。
//
// @人 mention 的用户 ID 类型统一走全局身份策略（union_id），见 identity.go。

// docxMentionObjTypeToString 云文档类型 int → 字符串（下载侧 obj-type 属性）
var docxMentionObjTypeToString = map[lark.DocxMentionObjType]string{
	lark.DocxMentionObjTypeDoc:      "doc",
	lark.DocxMentionObjTypeSheet:    "sheet",
	lark.DocxMentionObjTypeBitable:  "bitable",
	lark.DocxMentionObjTypeMindNote: "mindnote",
	lark.DocxMentionObjTypeFile:     "file",
	lark.DocxMentionObjTypeSlide:    "slide",
	lark.DocxMentionObjTypeWiki:     "wiki",
	lark.DocxMentionObjTypeDocx:     "docx",
}

// stringToDocxMentionObjType 字符串 → 云文档类型 int（上传侧解析 obj-type）
var stringToDocxMentionObjType = map[string]lark.DocxMentionObjType{
	"doc":      lark.DocxMentionObjTypeDoc,
	"sheet":    lark.DocxMentionObjTypeSheet,
	"bitable":  lark.DocxMentionObjTypeBitable,
	"mindnote": lark.DocxMentionObjTypeMindNote,
	"file":     lark.DocxMentionObjTypeFile,
	"slide":    lark.DocxMentionObjTypeSlide,
	"wiki":     lark.DocxMentionObjTypeWiki,
	"docx":     lark.DocxMentionObjTypeDocx,
}

// mentionObjTypeString 把云文档类型枚举转为字符串，未知类型兜底为新版文档 docx。
func mentionObjTypeString(t lark.DocxMentionObjType) string {
	if s, ok := docxMentionObjTypeToString[t]; ok {
		return s
	}
	return "docx"
}

// mentionObjTypeFromString 把字符串转为云文档类型枚举，未知字符串兜底为新版文档 docx。
func mentionObjTypeFromString(s string) lark.DocxMentionObjType {
	if t, ok := stringToDocxMentionObjType[strings.ToLower(strings.TrimSpace(s))]; ok {
		return t
	}
	return lark.DocxMentionObjTypeDocx
}

// xmlTextEscaper 转义 XML 标签内文本内容（< > &）
var xmlTextEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

// xmlAttrEscaper 转义 XML 属性值（在文本基础上额外转义 "）
var xmlAttrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

// xmlEscapeText 转义 <cite> 标签内文本
func xmlEscapeText(s string) string { return xmlTextEscaper.Replace(s) }

// xmlEscapeAttr 转义 <cite> 标签属性值
func xmlEscapeAttr(s string) string { return xmlAttrEscaper.Replace(s) }

// renderMentionUser 产出 @人 的 <cite type="user"> 标签。
// 标签文本用显示名（经 userNames 解析），解析失败兜底用 user-id 本身，
// 保证标签结构完整、ID 不丢、round-trip 不受权限影响。
func (p *Parser) renderMentionUser(e *lark.DocxTextElementMentionUser) string {
	uid := e.UserID
	name := uid
	if p.userNames != nil {
		if n, ok := p.userNames[uid]; ok && n != "" {
			name = n
		}
	}
	return fmt.Sprintf(`<cite type="user" user-id="%s">%s</cite>`,
		xmlEscapeAttr(uid), xmlEscapeText(name))
}

// renderMentionDoc 产出 @文档 的 <cite type="doc"> 标签，
// 完整保留 token + obj_type + url + title 四项（href 存解码后的可读形式）。
func renderMentionDoc(e *lark.DocxTextElementMentionDoc) string {
	href := utils.UnescapeURL(e.URL)
	return fmt.Sprintf(`<cite type="doc" doc-id="%s" obj-type="%s" href="%s">%s</cite>`,
		xmlEscapeAttr(e.Token), mentionObjTypeString(e.ObjType),
		xmlEscapeAttr(href), xmlEscapeText(e.Title))
}

// =============================================================
// 上传侧：识别行内 <cite> 标签 → MentionUser / MentionDoc
// =============================================================

// citePending 记录正在解析中的 <cite> 标签状态。
// goldmark 把行内 HTML 解析为扁平的兄弟节点（开标签 / 文本 / 闭标签各自独立），
// 因此用一个状态机：开标签时进入、闭标签时产出对应 mention 元素。
type citePending struct {
	kind    string // "user" | "doc"
	userID  string // type=user 的 user-id（union_id，与全局身份策略 userIDType 一致）
	token   string // type=doc 的 doc-id（云文档 token）
	objType lark.DocxMentionObjType
	url     string // type=doc 的 href（解码后的可读形式，产出时再 url-encode）
	text    strings.Builder
}

// handleCiteInline 处理 <cite> 相关的行内节点。
// 返回 true 表示该节点已被 cite 逻辑消费，调用方应 continue。
//   - <cite type=...> 开标签：进入 cite 状态
//   - </cite> 闭标签：产出 mention 元素
//   - cite 内部的任意节点：累积为显示文本，不单独成 element
//
// 「裸飞书链接不升级」：仅显式 <cite> 标签才转 mention，普通 [标题](url) 不受影响。
func (c *converter) handleCiteInline(child ast.Node, elements *[]*lark.DocxTextElement) bool {
	if raw, ok := child.(*ast.RawHTML); ok {
		tag := string(raw.Segments.Value(c.source))
		name, isClose, attrs := parseHTMLTag(tag)
		if name == "cite" {
			if isClose {
				c.flushCite(elements)
			} else {
				c.startCite(attrs)
			}
			return true
		}
	}
	if c.cite != nil {
		// cite 内部内容：累积为显示文本（标题 / 降级文案），不单独成 element
		c.cite.text.WriteString(extractInlineText(child, c.source))
		return true
	}
	return false
}

// startCite 根据开标签属性进入 cite 状态。未知 type 不进入（按普通文本处理）。
func (c *converter) startCite(attrs map[string]string) {
	switch attrs["type"] {
	case "user":
		c.cite = &citePending{kind: "user", userID: attrs["user-id"]}
	case "doc":
		c.cite = &citePending{
			kind:    "doc",
			token:   attrs["doc-id"],
			objType: mentionObjTypeFromString(attrs["obj-type"]),
			url:     attrs["href"],
		}
	default:
		c.cite = nil
	}
}

// flushCite 在闭标签处产出 mention 元素并清空状态。
// 标签内文本（显示名/标题）经 HTML 实体解码还原。缺关键 ID 时降级为纯文本。
func (c *converter) flushCite(elements *[]*lark.DocxTextElement) {
	if c.cite == nil {
		return
	}
	pending := c.cite
	c.cite = nil
	text := html.UnescapeString(pending.text.String())

	switch pending.kind {
	case "user":
		if pending.userID != "" {
			name := text
			if name == "" {
				name = pending.userID
			}
			if c.result.MentionUserNames == nil {
				c.result.MentionUserNames = make(map[string]string)
			}
			c.result.MentionUserNames[pending.userID] = name
			*elements = append(*elements, &lark.DocxTextElement{
				MentionUser: &lark.DocxTextElementMentionUser{UserID: pending.userID},
			})
			return
		}
	case "doc":
		if pending.token != "" {
			fallback := "FallbackToLink"
			*elements = append(*elements, &lark.DocxTextElement{
				MentionDoc: &lark.DocxTextElementMentionDoc{
					Token:        pending.token,
					ObjType:      pending.objType,
					URL:          utils.EscapeURL(pending.url),
					Title:        text,
					FallbackType: &fallback,
				},
			})
			return
		}
	}
	// 缺关键 ID：降级为纯文本，避免内容丢失
	if text != "" {
		*elements = append(*elements, &lark.DocxTextElement{
			TextRun: &lark.DocxTextElementTextRun{Content: text},
		})
	}
}
