package core

import (
	"strings"

	"github.com/chyroc/lark"
	"golang.org/x/net/html"
)

// htmlAttrEscape 转义 HTML 属性值中的 & 和 "（用于 <whiteboard> 标记的下载侧生成）。
func htmlAttrEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// convertHTMLWhiteboard 处理 round-trip 白板的 <whiteboard token=".."> 扩展标签。
//
// 协议形态（下载侧 ParseDocxBlockBoard 产出）：
//
//	<whiteboard token="T" href=".." width=".." height="..">
//	<img src="dir/whiteboard_T.png" alt="画板"/>
//	</whiteboard>
//
// token 直接作为 board 块身份（round-trip 复用原白板）；内层 <img> 仅为下载缩略图，
// 跳过不上传；href/width/height 上传时忽略。
func (c *converter) convertHTMLWhiteboard(n *html.Node) {
	token := getAttr(n, "token")
	if token == "" {
		// 无 token：降级为普通容器处理子节点（容错，避免内容丢失）
		c.walkHTMLChildrenGrouped(n)
		return
	}
	c.addTopBlock(&lark.DocxBlock{
		BlockType: lark.DocxBlockTypeBoard,
		Board:     &lark.DocxBlockBoard{Token: token},
	})
}

// isRoundTripBoard 判断块是否为 round-trip 白板（带 token）。
// 这类块在上传时复用原白板：diff 命中则原地保留；diff 判为新建则跳过（不能用 create
// 重建——会造空白板），故所有创建路径都用本判据排除它。
func isRoundTripBoard(b *lark.DocxBlock) bool {
	return b != nil &&
		b.BlockType == lark.DocxBlockTypeBoard &&
		b.Board != nil &&
		b.Board.Token != ""
}
