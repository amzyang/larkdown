package core_test

import (
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boardBlocksOf 提取转换结果中的所有 board 块。
func boardBlocksOf(result *core.ConvertResult) []*lark.DocxBlock {
	var boards []*lark.DocxBlock
	for _, b := range result.TopBlocks {
		if b != nil && b.BlockType == lark.DocxBlockTypeBoard {
			boards = append(boards, b)
		}
	}
	return boards
}

// TestBoardRoundTripIntegration 端到端集成：用 testdata/roundtrip.board.json（远程 blocks 含
// 白板 token=boardTOKEN），串联「下载 parser → markdown → 上传 md2blocks → diff」三个模块，
// 验证白板按 token 原地保留：diff 全 Equal（增量上传对该白板零 API 调用、不删不建、token 不变）。
func TestBoardRoundTripIntegration(t *testing.T) {
	doc, blocks := loadFixture(t, "roundtrip.board")

	// 1. 下载侧：blocks → markdown，应产出携带原 token 的 <whiteboard> 标记
	parser := core.NewParser(core.NewConfig("", "").Output, nil)
	md := parser.ParseDocxContent(doc, blocks)
	assert.Contains(t, md, `<whiteboard token="boardTOKEN"`,
		"下载产物应含 <whiteboard> 标记并携带原 board token")

	// 2. 上传侧：markdown → blocks，应解析出恰一个 board 块、复用原 token、缩略图不上传
	body := core.RemoveFirstHeading(md)
	result, err := core.ConvertMarkdownToDocxBlocks(body, "")
	require.NoError(t, err)

	boards := boardBlocksOf(result)
	require.Len(t, boards, 1, "应恰好解析出一个 board 块")
	assert.Equal(t, "boardTOKEN", boards[0].Board.Token, "应复用原 board token")
	assert.Empty(t, result.ImageIndices, "白板缩略图不应作为图片上传")

	// 3. diff：远程 vs 本地签名序列全 Equal → 白板原地保留，无 delete/insert/replace
	remoteSigs := remoteTopSignatures(doc, blocks)
	localSigs := localSignatures(result)
	ops := core.ComputeDiff(remoteSigs, localSigs)
	for _, op := range ops {
		assert.Equalf(t, core.DiffOpEqual, op.Type,
			"round-trip 应全 Equal，但出现 %v (remoteIdx=%d localIdx=%d)", op.Type, op.RemoteIdx, op.LocalIdx)
	}
}

// TestBoardDeletionHonoredIntegration 端到端集成：markdown 中删除白板（<whiteboard> 段不存在）后，
// diff 应对远程 board 块产出 Delete —— 即「markdown 是事实源，删掉才删」（尊重用户意图）。
func TestBoardDeletionHonoredIntegration(t *testing.T) {
	doc, blocks := loadFixture(t, "roundtrip.board")
	remoteSigs := remoteTopSignatures(doc, blocks)
	remoteBlocks := remoteTopBlocks(doc, blocks)

	// markdown 只保留文本，删除白板段
	result, err := core.ConvertMarkdownToDocxBlocks("Whiteboard:\n", "")
	require.NoError(t, err)
	localSigs := localSignatures(result)

	ops := core.ComputeDiff(remoteSigs, localSigs)
	foundBoardDelete := false
	for _, op := range ops {
		if op.Type == core.DiffOpDelete && remoteBlocks[op.RemoteIdx].BlockType == lark.DocxBlockTypeBoard {
			foundBoardDelete = true
		}
	}
	assert.True(t, foundBoardDelete, "markdown 删除白板后，diff 应对该 board 产出 Delete（尊重用户删除）")
}

// TestBoardReorderTriggersNonEqualDiff 端到端集成：白板在 markdown 中被移到不同相对位置时，
// 签名序列不再全 Equal —— 即位置变化能被 diff 感知，进而触发 reconcileBoardPositions 的 move 校正。
func TestBoardReorderTriggersNonEqualDiff(t *testing.T) {
	doc, blocks := loadFixture(t, "roundtrip.board")
	remoteSigs := remoteTopSignatures(doc, blocks) // 远程顺序：[text, board]

	// 本地把白板移到文本之前：[board, text]
	body := "<whiteboard token=\"boardTOKEN\" width=\"800\" height=\"600\">\n</whiteboard>\n\nWhiteboard:\n"
	result, err := core.ConvertMarkdownToDocxBlocks(body, "")
	require.NoError(t, err)
	localSigs := localSignatures(result)

	ops := core.ComputeDiff(remoteSigs, localSigs)
	allEqual := true
	for _, op := range ops {
		if op.Type != core.DiffOpEqual {
			allEqual = false
		}
	}
	assert.False(t, allEqual, "白板被重排后签名序列应不再全 Equal（diff 感知到位置变化）")
}
