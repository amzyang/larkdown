package core

import (
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

// boardsOf 提取转换结果中的所有 board 块
func boardsOf(result *ConvertResult) []*lark.DocxBlock {
	var boards []*lark.DocxBlock
	for _, b := range result.TopBlocks {
		if b != nil && b.BlockType == lark.DocxBlockTypeBoard {
			boards = append(boards, b)
		}
	}
	return boards
}

func TestWhiteboardMarkerParsing(t *testing.T) {
	md := "<whiteboard token=\"TKN123\" href=\"https://x.feishu.cn/docx/DOC?openbrd=1&amp;blockToken=TKN123#bid\" width=\"800\" height=\"600\">\n" +
		"<img src=\"doc/whiteboard_TKN123.png\" alt=\"画板\"/>\n" +
		"</whiteboard>\n"

	result, err := ConvertMarkdownToDocxBlocks(md, "")
	assert.NoError(t, err)

	boards := boardsOf(result)
	assert.Len(t, boards, 1, "应恰好产出一个 board 块")
	assert.Equal(t, "TKN123", boards[0].Board.Token)
	assert.Empty(t, result.ImageIndices, "内层 img 不应作为图片上传")
	assert.Empty(t, result.DescendantGroups, "不应生成 QuoteContainer")
}

func TestWhiteboardMarkerNoImage(t *testing.T) {
	// 下载失败/无图形态（仍可 round-trip）
	md := "<whiteboard token=\"TKN456\" href=\"https://x/board/TKN456\" width=\"800\" height=\"600\">\n</whiteboard>\n"

	result, err := ConvertMarkdownToDocxBlocks(md, "")
	assert.NoError(t, err)

	boards := boardsOf(result)
	assert.Len(t, boards, 1)
	assert.Equal(t, "TKN456", boards[0].Board.Token)
	assert.Empty(t, result.ImageIndices)
}

func TestIsRoundTripBoard(t *testing.T) {
	tokenBoard := &lark.DocxBlock{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "T"}}
	plantumlBoard := &lark.DocxBlock{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{}}
	textBlock := &lark.DocxBlock{BlockType: lark.DocxBlockTypeText}

	assert.True(t, isRoundTripBoard(tokenBoard))
	assert.False(t, isRoundTripBoard(plantumlBoard))
	assert.False(t, isRoundTripBoard(textBlock))
	assert.False(t, isRoundTripBoard(nil))
}

// TestBoardSignatureSymmetry 验证远程 board 块与本地解析出的 board 块产出相同签名，
// 这是 round-trip 白板被判为 DiffOpEqual（原地保留）的前提。
func TestBoardSignatureSymmetry(t *testing.T) {
	const token = "TKNsym"

	// 远程 board 块
	remote := &lark.DocxBlock{
		BlockID:   "blk1",
		BlockType: lark.DocxBlockTypeBoard,
		Board:     &lark.DocxBlockBoard{Token: token},
	}
	remoteSig := SignatureFromBlock(remote, nil)

	// 本地：从 markdown 解析（开标签须独占一行，符合 goldmark HTMLBlock type-7）
	md := "<whiteboard token=\"" + token + "\" width=\"800\" height=\"600\">\n</whiteboard>\n"
	result, err := ConvertMarkdownToDocxBlocks(md, "")
	assert.NoError(t, err)
	// 找到 board 所在的 TopBlocks 索引
	idx := -1
	for i, b := range result.TopBlocks {
		if b != nil && b.BlockType == lark.DocxBlockTypeBoard {
			idx = i
			break
		}
	}
	assert.NotEqual(t, -1, idx)
	localSig := SignatureFromLocalEntry(result, idx)

	assert.Equal(t, BlockSignature("board:"+token), remoteSig)
	assert.Equal(t, remoteSig, localSig, "远程与本地 board 签名必须对称")
}

func TestSelectFullUpdateDeletions(t *testing.T) {
	board := func(token string) *lark.DocxBlock {
		return &lark.DocxBlock{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: token}}
	}
	text := &lark.DocxBlock{BlockType: lark.DocxBlockTypeText}
	rootBlocks := []*lark.DocxBlock{
		text,        // 0
		text,        // 1
		board("T2"), // 2 引用中 → 保留
		text,        // 3
		board("T9"), // 4 未引用 → 删除
		board("T5"), // 5 引用中 → 保留
		text,        // 6
	}
	referenced := map[string]bool{"T2": true, "T5": true}
	del := selectFullUpdateDeletions(rootBlocks, nil, referenced)
	assert.Equal(t, []int{0, 1, 3, 4, 6}, del)
}

// TestCollectDeletions_PreservesReorderedBoard 验证删除路径的核心安全 guard：
// token 仍在本地 markdown 的白板（重排场景，LCS 可能误判为 delete）绝不删除；
// token 已不在本地的白板才真删除；空文本块仍跳过。
func TestCollectDeletions_PreservesReorderedBoard(t *testing.T) {
	emptyText := &lark.DocxBlock{BlockID: "bEmpty", BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{}}
	rootBlocks := []*lark.DocxBlock{
		{BlockID: "bX", BlockType: lark.DocxBlockTypeText, Text: &lark.DocxBlockText{Elements: []*lark.DocxTextElement{{TextRun: &lark.DocxTextElementTextRun{Content: "X"}}}}}, // 0
		{BlockID: "bBoard", BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "T"}},                                                                        // 1 重排：token 仍在本地 → 保留
		{BlockID: "bGone", BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "TGONE"}},                                                                     // 2 真删：token 不在本地
		emptyText, // 3 空文本块 → 跳过
	}
	pairedOps := []PairedOp{
		{Type: PairedOpDelete, RemoteIdx: 0},
		{Type: PairedOpDelete, RemoteIdx: 1},
		{Type: PairedOpDelete, RemoteIdx: 2},
		{Type: PairedOpDelete, RemoteIdx: 3},
	}
	localTokens := map[string]bool{"T": true} // 仅 T 仍被 markdown 引用

	dels := collectDeletions(pairedOps, rootBlocks, nil, localTokens)
	assert.Equal(t, []string{"bX", "bGone"}, dels)
	assert.NotContains(t, dels, "bBoard", "token 仍在本地的白板（重排）绝不删除")
}

// TestPlanBoardMoves_ReorderedBoardFoundByToken 回归测试：白板被重排（远程顺序 [P1,P2,board]、
// 本地顺序 [board,P1,P2]）时，白板是 diff 的非 Equal 元素，必须按 token 匹配到远程 ID 并规划
// 移到开头。若退回"靠 Equal 映射"会漏找白板 → 返回空 moves（bug），本测试守住该回归。
func TestPlanBoardMoves_ReorderedBoardFoundByToken(t *testing.T) {
	const docID = "DOC"
	mkText := func(id, s string) *lark.DocxBlock {
		return &lark.DocxBlock{BlockID: id, ParentID: docID, BlockType: lark.DocxBlockTypeText,
			Text: &lark.DocxBlockText{Elements: []*lark.DocxTextElement{{TextRun: &lark.DocxTextElementTextRun{Content: s}}}}}
	}
	p1 := mkText("p1", "para-ONE")
	p2 := mkText("p2", "para-TWO")
	board := &lark.DocxBlock{BlockID: "bd", ParentID: docID, BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "T"}}
	current := []*lark.DocxBlock{p1, p2, board} // 远程：白板在末尾
	blockMap := map[string]*lark.DocxBlock{"p1": p1, "p2": p2, "bd": board}

	// 本地：白板在最前
	local, err := ConvertMarkdownToDocxBlocks("<whiteboard token=\"T\">\n</whiteboard>\n\npara-ONE\n\npara-TWO\n", "")
	if err != nil {
		t.Fatal(err)
	}

	moves := planEntityMoves(current, blockMap, local, docID)
	if assert.Len(t, moves, 1, "重排白板应规划 1 次移动") {
		assert.Equal(t, "bd", moves[0].remoteID, "应按 token 匹配到远程白板 ID")
		assert.Equal(t, docID, moves[0].anchorID, "白板应移到文档开头（anchor=documentID）")
	}
}

// TestPlanBoardMoves_InPlaceNoMove 白板已在期望位置 → 不规划移动（增量未重排常态，零 API 调用）。
func TestPlanBoardMoves_InPlaceNoMove(t *testing.T) {
	const docID = "DOC"
	board := &lark.DocxBlock{BlockID: "bd", ParentID: docID, BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "T"}}
	current := []*lark.DocxBlock{board}
	blockMap := map[string]*lark.DocxBlock{"bd": board}
	local, err := ConvertMarkdownToDocxBlocks("<whiteboard token=\"T\">\n</whiteboard>\n", "")
	if err != nil {
		t.Fatal(err)
	}
	assert.Nil(t, planEntityMoves(current, blockMap, local, docID), "白板已就位不应规划移动")
}

func TestMergeDeleteRanges(t *testing.T) {
	// [0,1,3,4,6] → 从大到小合并连续区间为左闭右开
	assert.Equal(t, [][2]int{{6, 7}, {3, 5}, {0, 2}}, mergeDeleteRanges([]int{0, 1, 3, 4, 6}))
	assert.Nil(t, mergeDeleteRanges(nil))
	assert.Equal(t, [][2]int{{2, 3}}, mergeDeleteRanges([]int{2}))
}
