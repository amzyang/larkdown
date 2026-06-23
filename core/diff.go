package core

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/amzyang/larkdown/utils"
	"github.com/chyroc/lark"
)

// BlockSignature 块内容签名，用于 diff 比对
type BlockSignature string

// DiffOpType diff 操作类型
type DiffOpType int

const (
	DiffOpEqual  DiffOpType = iota // 块未变化
	DiffOpDelete                   // 远程有、本地无
	DiffOpInsert                   // 本地有、远程无
)

// DiffOp 单个 diff 操作
type DiffOp struct {
	Type      DiffOpType
	RemoteIdx int // 远程块索引（Delete/Equal 有效）
	LocalIdx  int // 本地块索引（Insert/Equal 有效）
}

// ChangeRegion 连续的非 Equal 操作区域
type ChangeRegion struct {
	DeleteIndices    []int // 远程块索引列表（待删除）
	InsertIndices    []int // 本地块索引列表（待插入）
	RemoteStartIndex int   // 该区域在远程块列表中的起始位置
}

// PairedOpType 配对操作类型
type PairedOpType int

const (
	PairedOpReplace       PairedOpType = iota // 原地更新（batch_update PATCH，保 block_id）
	PairedOpDelete                            // 仅删除
	PairedOpInsert                            // 仅插入
	PairedOpDocsAIReplace                     // docs_ai block_replace 原地替换（跨类型 text-like，block_id 可能变）
)

// PairedOp 配对后的操作
type PairedOp struct {
	Type      PairedOpType
	RemoteIdx int // 远程块索引（Replace/Delete 有效）
	LocalIdx  int // 本地块索引（Replace/Insert 有效）
}

// isTextLikeBlock 判断块类型是否为 text-like（可通过 UpdateTextElements 原地更新）
func isTextLikeBlock(bt lark.DocxBlockType) bool {
	switch bt {
	case lark.DocxBlockTypeText,
		lark.DocxBlockTypeHeading1, lark.DocxBlockTypeHeading2, lark.DocxBlockTypeHeading3,
		lark.DocxBlockTypeHeading4, lark.DocxBlockTypeHeading5, lark.DocxBlockTypeHeading6,
		lark.DocxBlockTypeHeading7, lark.DocxBlockTypeHeading8, lark.DocxBlockTypeHeading9,
		lark.DocxBlockTypeBullet, lark.DocxBlockTypeOrdered,
		lark.DocxBlockTypeQuote,
		lark.DocxBlockTypeTodo:
		return true
	}
	return false
}

// isDescendantBlock 判断块类型是否为 descendant 结构（Table, Callout, QuoteContainer 等）
func isDescendantBlock(bt lark.DocxBlockType) bool {
	switch bt {
	case lark.DocxBlockTypeTable, lark.DocxBlockTypeCallout, lark.DocxBlockTypeQuoteContainer:
		return true
	}
	return false
}

// isEmptyTextBlock 判断块是否为「空文本块」（飞书空段落）：
// 类型为 Text 且无任何可见内容（无非空 TextRun、无公式/Mention）。
func isEmptyTextBlock(b *lark.DocxBlock) bool {
	if b == nil || b.BlockType != lark.DocxBlockTypeText {
		return false
	}
	if len(b.Children) > 0 {
		return false
	}
	if b.Text == nil {
		return true
	}
	for _, e := range b.Text.Elements {
		if e.TextRun != nil && strings.TrimSpace(e.TextRun.Content) != "" {
			return false
		}
		if e.Equation != nil || e.MentionUser != nil || e.MentionDoc != nil {
			return false
		}
	}
	return true
}

// getBlockText 获取块的 DocxBlockText（根据 BlockType 选择对应字段）
func getBlockText(block *lark.DocxBlock) *lark.DocxBlockText {
	switch block.BlockType {
	case lark.DocxBlockTypeText:
		return block.Text
	case lark.DocxBlockTypeHeading1:
		return block.Heading1
	case lark.DocxBlockTypeHeading2:
		return block.Heading2
	case lark.DocxBlockTypeHeading3:
		return block.Heading3
	case lark.DocxBlockTypeHeading4:
		return block.Heading4
	case lark.DocxBlockTypeHeading5:
		return block.Heading5
	case lark.DocxBlockTypeHeading6:
		return block.Heading6
	case lark.DocxBlockTypeHeading7:
		return block.Heading7
	case lark.DocxBlockTypeHeading8:
		return block.Heading8
	case lark.DocxBlockTypeHeading9:
		return block.Heading9
	case lark.DocxBlockTypeBullet:
		return block.Bullet
	case lark.DocxBlockTypeOrdered:
		return block.Ordered
	case lark.DocxBlockTypeCode:
		return block.Code
	case lark.DocxBlockTypeQuote:
		return block.Quote
	case lark.DocxBlockTypeTodo:
		return block.Todo
	}
	return nil
}

// styleMarkers 返回 TextElementStyle 的规范化标记串（顺序固定，确保两侧可比）
func styleMarkers(s *lark.DocxTextElementStyle) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	if s.Bold {
		b.WriteString("|B")
	}
	if s.Italic {
		b.WriteString("|I")
	}
	if s.Strikethrough {
		b.WriteString("|S")
	}
	if s.Underline {
		b.WriteString("|U")
	}
	if s.InlineCode {
		b.WriteString("|C")
	}
	if s.Link != nil {
		// 远程 link.URL 为 percent-encoded（https%3A%2F%2F…），本地为解码态；归一到解码态。
		b.WriteString("|L:")
		b.WriteString(utils.UnescapeURL(s.Link.URL))
	}
	return b.String()
}

// canonicalTextContent 计算 DocxBlockText 的规范化文本内容
// 拼接每个 element 的 content + style 标记，确保格式变化也能被检测到
func canonicalTextContent(bt *lark.DocxBlockText) string {
	if bt == nil {
		return ""
	}
	var buf strings.Builder
	for _, elem := range bt.Elements {
		if elem.TextRun != nil {
			s := elem.TextRun.TextElementStyle
			content := elem.TextRun.Content
			// emphasis（非 InlineCode）样式：把内容两端空白移到样式标记之外，
			// 使「空白在样式内」（远程，如 "x "+Bold）与「空白在样式外」（本地，
			// parser 外提后 "x"+Bold + " "）归一为同一签名。InlineCode 不归一。
			hoist := s != nil && !s.InlineCode &&
				(s.Bold || s.Italic || s.Strikethrough || s.Underline || s.Link != nil)
			if hoist {
				t := strings.TrimLeft(content, " \t")
				lead := content[:len(content)-len(t)]
				core := strings.TrimRight(t, " \t")
				trail := t[len(core):]
				if core != "" {
					buf.WriteString(lead)
					buf.WriteString(core)
					buf.WriteString(styleMarkers(s))
					buf.WriteString(trail)
					continue
				}
				// 全空白：无标记，原样
				buf.WriteString(content)
				continue
			}
			buf.WriteString(content)
			if s != nil {
				buf.WriteString(styleMarkers(s))
			}
		}
		if elem.Equation != nil {
			buf.WriteString("$")
			buf.WriteString(elem.Equation.Content)
			buf.WriteString("$")
		}
		if elem.MentionUser != nil {
			buf.WriteString("@U:")
			buf.WriteString(elem.MentionUser.UserID)
		}
		if elem.MentionDoc != nil {
			buf.WriteString("@D:")
			buf.WriteString(elem.MentionDoc.Token)
		}
	}
	// 文本内容首尾的空格/制表符在 markdown round-trip 中会被 goldmark strip 掉
	// （行首尾空白不保留），故归一时去掉，避免不可见空白导致签名误判。
	// 在拼接 |done/|lang 等后缀之前 trim，保证后缀不被影响。
	var out strings.Builder
	out.WriteString(strings.Trim(buf.String(), " \t"))
	// 包含 style 信息（Done 状态、语言）
	if bt.Style != nil {
		if bt.Style.Done {
			out.WriteString("|done")
		}
		if bt.Style.Language != 0 {
			fmt.Fprintf(&out, "|lang:%d", bt.Style.Language)
		}
	}
	return out.String()
}

// SignatureFromBlock 计算远程块的签名
func SignatureFromBlock(block *lark.DocxBlock, blockMap map[string]*lark.DocxBlock) BlockSignature {
	bt := block.BlockType

	// 媒体实体（图片/附件/视频）优先：token-aware 签名 media:<token>，round-trip 按 token 与本地匹配，
	// 同 token → Equal → 原地保留（复用远程块、不删不重传）。须在「带 children → descendant」短路之前，
	// 因为 File 在远程被 View(33) 包裹（view 有 children），entityRefFromBlock 穿透 view 取内层 token。
	if ref, ok := entityRefFromBlock(block, blockMap); ok {
		return BlockSignature("media:" + ref.token)
	}

	// 带 children 的块（folded text 等）按 descendant 处理
	if len(block.Children) > 0 && blockMap != nil {
		return signatureFromDescendants(block, blockMap)
	}

	// Quote(15) 旧版引用块：上传会重建为 QuoteContainer(34)，
	// 故按等价 QuoteContainer 树计算签名，保持 round-trip 对称。
	if bt == lark.DocxBlockTypeQuote {
		return signatureFromQuoteBlock(block)
	}

	// Equation(16) 旧版独立公式块（已下线）：上传会重建为 Text(2) 含公式 element，
	// 故签名按 Text 块对齐，避免落入 unknown 兜底导致永不匹配。
	if bt == lark.DocxBlockTypeEquation {
		return BlockSignature(fmt.Sprintf("%d:%s", lark.DocxBlockTypeText, canonicalTextContent(block.Equation)))
	}

	// text-like 块和 Code 块
	if isTextLikeBlock(bt) || bt == lark.DocxBlockTypeCode {
		text := getBlockText(block)
		return BlockSignature(fmt.Sprintf("%d:%s", bt, canonicalTextContent(text)))
	}

	// Divider
	if bt == lark.DocxBlockTypeDivider {
		return "divider"
	}

	// AddOns (Mermaid 等)
	if bt == lark.DocxBlockTypeAddOns && block.AddOns != nil {
		return BlockSignature(fmt.Sprintf("40:%s", addOnsSignatureContent(block.AddOns.Record)))
	}

	// Board (画板)：token-aware 签名，使 round-trip 的白板按 token 与本地匹配，
	// 同 token → Equal → 原地保留（白板/token 不被删除重建）。
	if bt == lark.DocxBlockTypeBoard {
		token := ""
		if block.Board != nil {
			token = block.Board.Token
		}
		return BlockSignature("board:" + token)
	}

	// Descendant 块 (Table, Callout, QuoteContainer 等) → 序列化后代结构
	if isDescendantBlock(bt) && blockMap != nil {
		return signatureFromDescendants(block, blockMap)
	}

	// 其他未知类型 → 使用类型+BlockID（永远不匹配其他块）
	return BlockSignature(fmt.Sprintf("unknown:%d:%s", bt, block.BlockID))
}

// signatureFromDescendants 计算 descendant 块的签名
func signatureFromDescendants(block *lark.DocxBlock, blockMap map[string]*lark.DocxBlock) BlockSignature {
	data := serializeBlockTree(block, blockMap)
	hash := sha256.Sum256(data)
	return BlockSignature(fmt.Sprintf("desc:%x", hash[:16]))
}

// serializeBlockTree 递归序列化块及其后代
func serializeBlockTree(block *lark.DocxBlock, blockMap map[string]*lark.DocxBlock) []byte {
	type serBlock struct {
		BlockType lark.DocxBlockType `json:"t"`
		Content   string             `json:"c,omitempty"`
		Children  []json.RawMessage  `json:"ch,omitempty"`
	}

	sb := serBlock{BlockType: block.BlockType}

	// 提取内容
	if text := getBlockText(block); text != nil {
		sb.Content = canonicalTextContent(text)
	}

	// 递归子块
	for _, childID := range block.Children {
		if child, ok := blockMap[childID]; ok {
			childData := serializeBlockTree(child, blockMap)
			sb.Children = append(sb.Children, json.RawMessage(childData))
		}
	}

	data, _ := json.Marshal(sb)
	return data
}

// addOnsSignatureContent 提取 AddOns.Record 中与内容相关的图源（data 字段），
// 避免比对服务端不可控的完整 JSON（额外字段 / key 顺序），与下载侧 ParseDocxBlockAddOns 取值一致。
func addOnsSignatureContent(record string) string {
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(record), &r); err != nil {
		return record
	}
	return r.Data
}

// signatureFromQuoteBlock 把旧版 Quote(15) 块按等价 QuoteContainer(34) 树计算签名，
// 与上传侧 convertQuoteContainer 的产物（单 Text 子块的 QuoteContainer）对齐。
func signatureFromQuoteBlock(block *lark.DocxBlock) BlockSignature {
	const containerID, textID = "q_container", "q_text"
	blockMap := map[string]*lark.DocxBlock{
		containerID: {
			BlockID:        containerID,
			BlockType:      lark.DocxBlockTypeQuoteContainer,
			Children:       []string{textID},
			QuoteContainer: &lark.DocxBlockQuoteContainer{},
		},
		textID: {
			BlockID:   textID,
			BlockType: lark.DocxBlockTypeText,
			Text:      block.Quote,
		},
	}
	data := serializeBlockTree(blockMap[containerID], blockMap)
	hash := sha256.Sum256(data)
	return BlockSignature(fmt.Sprintf("desc:%x", hash[:16]))
}

// signatureFromLocalDescendant 计算本地 descendant group 的签名，
// 复用 serializeBlockTree 使其与远程 signatureFromDescendants 完全对称（剔除临时 ID 等抖动字段）。
func signatureFromLocalDescendant(dg *DescendantGroup) BlockSignature {
	blockMap := make(map[string]*lark.DocxBlock, len(dg.Descendants))
	for _, b := range dg.Descendants {
		blockMap[b.BlockID] = b
	}
	var data []byte
	if len(dg.ChildrenIDs) == 1 {
		// 单根（table/callout/quote_container/folded）：直接序列化根块，与远程顶层块对称。
		root, ok := blockMap[dg.ChildrenIDs[0]]
		if !ok {
			return "desc:nil"
		}
		data = serializeBlockTree(root, blockMap)
	} else {
		// 多根（嵌套 list）：包一层虚拟容器序列化，仅保证本地自身幂等（消除临时 ID 抖动）。
		// 远程为 N 个独立顶层块，数量天然不匹配，属已知限制。
		data = serializeBlockTree(&lark.DocxBlock{Children: dg.ChildrenIDs}, blockMap)
	}
	hash := sha256.Sum256(data)
	return BlockSignature(fmt.Sprintf("desc:%x", hash[:16]))
}

// SignatureFromLocalEntry 计算本地转换结果中第 idx 个顶级块的签名
func SignatureFromLocalEntry(result *ConvertResult, idx int) BlockSignature {
	// 检查是否为 descendant 结构（使用预构建的 map）
	if dg := result.descendantGroupAt(idx); dg != nil {
		return signatureFromLocalDescendant(dg)
	}

	block := result.TopBlocks[idx]
	if block == nil {
		return "nil"
	}

	bt := block.BlockType

	if isTextLikeBlock(bt) || bt == lark.DocxBlockTypeCode {
		text := getBlockText(block)
		return BlockSignature(fmt.Sprintf("%d:%s", bt, canonicalTextContent(text)))
	}

	// 媒体实体：token 已由 resolveEntityTokens 按文件名前缀解析写入本地块；与远程 media:<token> 对称。
	// 未解析（新增/手动添加的本地资源）→ media:new:<idx> 命名空间隔离，永不误配远程、必触发上传。
	if bt == lark.DocxBlockTypeImage || bt == lark.DocxBlockTypeFile {
		if t := localEntityToken(block); t != "" {
			return BlockSignature("media:" + t)
		}
		return BlockSignature(fmt.Sprintf("media:new:%d", idx))
	}

	if bt == lark.DocxBlockTypeDivider {
		return "divider"
	}

	if bt == lark.DocxBlockTypeAddOns && block.AddOns != nil {
		return BlockSignature(fmt.Sprintf("40:%s", addOnsSignatureContent(block.AddOns.Record)))
	}

	if bt == lark.DocxBlockTypeBoard {
		// round-trip 白板（带 token）：与远程 board:<token> 对称
		if block.Board != nil && block.Board.Token != "" {
			return BlockSignature("board:" + block.Board.Token)
		}
		// 无 token 的 plantuml 白板：命名空间隔离，永不误配远程 token
		return BlockSignature("board:plantuml:" + boardCodeHash(result, idx))
	}

	return BlockSignature(fmt.Sprintf("local:%d:%d", bt, idx))
}

// boardCodeHash 取本地第 idx 个 board 块对应的 PlantUML 源码哈希（无 token 白板专用）。
// 复用 canonicalBoardSourceHash，与画板映射记录（board_manifest.go）的 key 口径同源。
func boardCodeHash(result *ConvertResult, idx int) string {
	for i, bIdx := range result.BoardIndices {
		if bIdx == idx {
			return canonicalBoardSourceHash(result.BoardCodes[i])
		}
	}
	// 找不到源码（理论不应发生）：用位置兜底，保证永不误配远程
	return fmt.Sprintf("idx:%d", idx)
}

// ComputeDiff 基于 LCS 计算远程和本地签名序列的 diff
func ComputeDiff(remote, local []BlockSignature) []DiffOp {
	m, n := len(remote), len(local)

	// DP 表
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if remote[i-1] == local[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// 回溯生成操作序列
	var ops []DiffOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && remote[i-1] == local[j-1] {
			ops = append(ops, DiffOp{Type: DiffOpEqual, RemoteIdx: i - 1, LocalIdx: j - 1})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, DiffOp{Type: DiffOpInsert, LocalIdx: j - 1})
			j--
		} else {
			ops = append(ops, DiffOp{Type: DiffOpDelete, RemoteIdx: i - 1})
			i--
		}
	}

	// 反转（回溯是逆序的）
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	return ops
}

// GroupChangeRegions 将连续的非 Equal 操作分组为 ChangeRegion
func GroupChangeRegions(ops []DiffOp) []ChangeRegion {
	var regions []ChangeRegion
	var current *ChangeRegion

	// 追踪远程索引位置
	nextRemoteIdx := 0

	for _, op := range ops {
		if op.Type == DiffOpEqual {
			if current != nil {
				regions = append(regions, *current)
				current = nil
			}
			nextRemoteIdx = op.RemoteIdx + 1
			continue
		}

		if current == nil {
			current = &ChangeRegion{RemoteStartIndex: nextRemoteIdx}
		}

		switch op.Type {
		case DiffOpDelete:
			current.DeleteIndices = append(current.DeleteIndices, op.RemoteIdx)
			nextRemoteIdx = op.RemoteIdx + 1
		case DiffOpInsert:
			current.InsertIndices = append(current.InsertIndices, op.LocalIdx)
		}
	}

	if current != nil {
		regions = append(regions, *current)
	}

	return regions
}

// PairBlocks 在 ChangeRegion 内配对 old/new 块。docsAIEnabled 控制是否启用 docs_ai block_replace
// 这一中间档（无 user_access_token 时应传 false，回退原删除+重建行为）。
func PairBlocks(region ChangeRegion, remoteBlocks []*lark.DocxBlock, localResult *ConvertResult, docsAIEnabled bool) []PairedOp {
	var ops []PairedOp

	delIdx := 0
	insIdx := 0

	for delIdx < len(region.DeleteIndices) && insIdx < len(region.InsertIndices) {
		remIdx := region.DeleteIndices[delIdx]
		locIdx := region.InsertIndices[insIdx]

		remoteBlock := remoteBlocks[remIdx]
		localBlock := localResult.TopBlocks[locIdx]

		switch {
		case canReplace(remoteBlock, localBlock, locIdx, localResult):
			// 第一档：batch_update PATCH 原地更新，保留 block_id
			ops = append(ops, PairedOp{Type: PairedOpReplace, RemoteIdx: remIdx, LocalIdx: locIdx})
			delIdx++
			insIdx++
		case canDocsAIReplace(remoteBlock, localBlock, locIdx, localResult, docsAIEnabled):
			// 第二档：docs_ai block_replace 整块替换（跨类型/容器块）
			ops = append(ops, PairedOp{Type: PairedOpDocsAIReplace, RemoteIdx: remIdx, LocalIdx: locIdx})
			delIdx++
			insIdx++
		default:
			// 第三档：无法原地替换，先删除（insert 留待配下一个 delete 或单独插入）
			ops = append(ops, PairedOp{Type: PairedOpDelete, RemoteIdx: remIdx})
			delIdx++
		}
	}

	// 剩余的删除
	for delIdx < len(region.DeleteIndices) {
		ops = append(ops, PairedOp{Type: PairedOpDelete, RemoteIdx: region.DeleteIndices[delIdx]})
		delIdx++
	}

	// 剩余的插入
	for insIdx < len(region.InsertIndices) {
		ops = append(ops, PairedOp{Type: PairedOpInsert, LocalIdx: region.InsertIndices[insIdx]})
		insIdx++
	}

	return ops
}

// blockTypeName 返回 DocxBlockType 的可读名称
func blockTypeName(bt lark.DocxBlockType) string {
	names := map[lark.DocxBlockType]string{
		lark.DocxBlockTypePage:           "Page",
		lark.DocxBlockTypeText:           "Text",
		lark.DocxBlockTypeHeading1:       "H1",
		lark.DocxBlockTypeHeading2:       "H2",
		lark.DocxBlockTypeHeading3:       "H3",
		lark.DocxBlockTypeHeading4:       "H4",
		lark.DocxBlockTypeHeading5:       "H5",
		lark.DocxBlockTypeHeading6:       "H6",
		lark.DocxBlockTypeHeading7:       "H7",
		lark.DocxBlockTypeHeading8:       "H8",
		lark.DocxBlockTypeHeading9:       "H9",
		lark.DocxBlockTypeBullet:         "Bullet",
		lark.DocxBlockTypeOrdered:        "Ordered",
		lark.DocxBlockTypeCode:           "Code",
		lark.DocxBlockTypeQuote:          "Quote",
		lark.DocxBlockTypeEquation:       "Equation",
		lark.DocxBlockTypeTodo:           "Todo",
		lark.DocxBlockTypeBitable:        "Bitable",
		lark.DocxBlockTypeCallout:        "Callout",
		lark.DocxBlockTypeDivider:        "Divider",
		lark.DocxBlockTypeFile:           "File",
		lark.DocxBlockTypeImage:          "Image",
		lark.DocxBlockTypeTable:          "Table",
		lark.DocxBlockTypeTableCell:      "TableCell",
		lark.DocxBlockTypeQuoteContainer: "QuoteContainer",
		lark.DocxBlockTypeAddOns:         "AddOns",
		lark.DocxBlockTypeBoard:          "Board",
	}
	if name, ok := names[bt]; ok {
		return name
	}
	return fmt.Sprintf("Type(%d)", bt)
}

// blockTextPreview 提取块文本的前 maxLen 字符作为预览
func blockTextPreview(block *lark.DocxBlock, maxLen int) string {
	if block == nil {
		return ""
	}
	text := getBlockText(block)
	if text == nil {
		return ""
	}
	var buf strings.Builder
	for _, elem := range text.Elements {
		if elem.TextRun != nil {
			buf.WriteString(elem.TextRun.Content)
		}
	}
	content := buf.String()
	if len([]rune(content)) > maxLen {
		return string([]rune(content)[:maxLen]) + "..."
	}
	return content
}

// classifyReplaceKind 分类 replace 操作类型
func classifyReplaceKind(remote, local *lark.DocxBlock) string {
	if remote.BlockType == lark.DocxBlockTypeImage && local.BlockType == lark.DocxBlockTypeImage {
		return "image"
	}
	if remote.BlockType == lark.DocxBlockTypeFile && local.BlockType == lark.DocxBlockTypeFile {
		return "file"
	}
	if remote.BlockType == lark.DocxBlockTypeCode {
		return "code"
	}
	if remote.BlockType == lark.DocxBlockTypeTodo {
		return "todo"
	}
	return "text"
}

// findImagePath 从 ConvertResult 查找对应 localIdx 的图片路径
func findImagePath(result *ConvertResult, localIdx int) string {
	for i, imgIdx := range result.ImageIndices {
		if imgIdx == localIdx {
			return result.ImagePaths[i]
		}
	}
	return ""
}

// findFilePath 从 ConvertResult 查找对应 localIdx 的文件路径
func findFilePath(result *ConvertResult, localIdx int) string {
	for i, fileIdx := range result.FileIndices {
		if fileIdx == localIdx {
			return result.FilePaths[i]
		}
	}
	return ""
}

// canReplace 判断两个块是否可以原地更新
func canReplace(remote *lark.DocxBlock, local *lark.DocxBlock, localIdx int, result *ConvertResult) bool {
	if local == nil {
		return false
	}

	// Descendant 块不支持原地更新
	if result.descendantGroupAt(localIdx) != nil {
		return false
	}
	if isDescendantBlock(remote.BlockType) {
		return false
	}

	// 同类型的 text-like 块可以替换
	if remote.BlockType == local.BlockType && isTextLikeBlock(remote.BlockType) {
		return true
	}

	// Code 块可以替换
	if remote.BlockType == lark.DocxBlockTypeCode && local.BlockType == lark.DocxBlockTypeCode {
		return true
	}

	// Image 块可以替换
	if remote.BlockType == lark.DocxBlockTypeImage && local.BlockType == lark.DocxBlockTypeImage {
		return true
	}

	// File 块可以替换
	if remote.BlockType == lark.DocxBlockTypeFile && local.BlockType == lark.DocxBlockTypeFile {
		return true
	}

	// Board 永不原地 Replace：同 token 已是 Equal 不会进 region；不同 token 进 region 时
	// 也只能 delete+insert（飞书 batch_update 无 ReplaceBoard 操作），故落到下方 return false。
	return false
}

// isMediaBlock 判断是否图片/文件块。
func isMediaBlock(bt lark.DocxBlockType) bool {
	return bt == lark.DocxBlockTypeImage || bt == lark.DocxBlockTypeFile
}

// canDocsAIReplace 判断变更块能否用 docs_ai block_replace 原地替换。三档分流的中间档：
// canReplace（batch_update 保 block_id）优先；此函数覆盖 canReplace 处理不了、但 docs_ai 能整块
// 替换的情况。需 docsAIEnabled（user_access_token 可用），否则回退删除重建。
//
// 当前仅放开「跨类型 text-like 互转」（text/heading/bullet/ordered/quote/todo 之间）。排除：
//   - 画板 / AddOns：docs_ai 无法表达；
//   - 媒体实体：走保留 / replace_image / replace_file；
//   - 容器块 table/callout/quote_container：docs_ai markdown 模式下 callout 须用 XML（markdown 会被
//     当成普通引用块）、table 的合并/列宽/背景色无法用 markdown 表达，故暂走删除重建，待引入
//     DescendantGroup→XML 序列化后再开放。
func canDocsAIReplace(remote, local *lark.DocxBlock, localIdx int, result *ConvertResult, docsAIEnabled bool) bool {
	if !docsAIEnabled || local == nil {
		return false
	}
	if canReplace(remote, local, localIdx, result) {
		return false // batch_update 优先
	}
	// 容器块：docs_ai markdown 表达受限，暂走删除重建（见函数注释）
	if isDescendantBlock(remote.BlockType) || result.descendantGroupAt(localIdx) != nil {
		return false
	}
	// 画板 / AddOns：docs_ai 无法表达
	if remote.BlockType == lark.DocxBlockTypeBoard || local.BlockType == lark.DocxBlockTypeBoard {
		return false
	}
	if remote.BlockType == lark.DocxBlockTypeAddOns || local.BlockType == lark.DocxBlockTypeAddOns {
		return false
	}
	// 媒体实体：走保留 / replace_image / replace_file
	if isRoundTripEntity(local) || isMediaBlock(remote.BlockType) || isMediaBlock(local.BlockType) {
		return false
	}
	// 仅跨类型 text-like（同类型已被 canReplace 拦截走 batch_update）
	return isTextLikeBlock(remote.BlockType) && isTextLikeBlock(local.BlockType)
}
