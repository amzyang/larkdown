package core

import (
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

// mkText 构造带内容的远程 text 块。
func mkRemoteText(id, parent, s string) *lark.DocxBlock {
	return &lark.DocxBlock{BlockID: id, ParentID: parent, BlockType: lark.DocxBlockTypeText,
		Text: &lark.DocxBlockText{Elements: []*lark.DocxTextElement{{TextRun: &lark.DocxTextElementTextRun{Content: s}}}}}
}

func mkLocalText(s string) *lark.DocxBlock {
	return &lark.DocxBlock{BlockType: lark.DocxBlockTypeText,
		Text: &lark.DocxBlockText{Elements: []*lark.DocxTextElement{{TextRun: &lark.DocxTextElementTextRun{Content: s}}}}}
}

// TestSelectFullUpdateDeletions_PreservesMediaEntities 全量上传保留被引用的图片/文件实体（含 view 包裹的 file），删除未引用的。
func TestSelectFullUpdateDeletions_PreservesMediaEntities(t *testing.T) {
	innerFile := &lark.DocxBlock{BlockID: "inF", BlockType: lark.DocxBlockTypeFile, File: &lark.DocxBlockFile{Token: "FB"}}
	view := &lark.DocxBlock{BlockID: "vw", BlockType: lark.DocxBlockTypeView, Children: []string{"inF"}}
	blockMap := map[string]*lark.DocxBlock{"inF": innerFile, "vw": view}

	rootBlocks := []*lark.DocxBlock{
		mkRemoteText("t0", "DOC", "x"), // 0 删
		{BlockID: "im1", BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "IA"}}, // 1 引用 → 保留
		view, // 2 view 包裹 file（token FB）引用 → 保留
		{BlockID: "im2", BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "IC"}}, // 3 未引用 → 删
	}
	referenced := map[string]bool{"IA": true, "FB": true}

	del := selectFullUpdateDeletions(rootBlocks, blockMap, referenced)
	assert.Equal(t, []int{0, 3}, del, "保留被引用图片与 view 包裹文件，删除文本与未引用图片")
}

// TestSelectFullUpdateDeletions_PreservesBoard 全量上传保留被引用的白板（token 仍在本地），删除未引用白板与其他块。
func TestSelectFullUpdateDeletions_PreservesBoard(t *testing.T) {
	rootBlocks := []*lark.DocxBlock{
		mkRemoteText("t0", "DOC", "x"), // 0 删
		{BlockID: "wb1", BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "WA"}}, // 1 引用 → 保留
		{BlockID: "wb2", BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "WC"}}, // 2 未引用 → 删
	}
	referenced := map[string]bool{"WA": true}

	// blockMap 传 nil：Board 经 isRoundTripBoard 取 token、text 非媒体实体，均不访问 blockMap
	del := selectFullUpdateDeletions(rootBlocks, nil, referenced)
	assert.Equal(t, []int{0, 2}, del, "保留被引用白板，删除文本与未引用白板")
}

// TestCollectDeletions_PreservesReorderedImage 图片被重排（LCS 判 delete）时，token 仍在本地 → 绝不删除。
func TestCollectDeletions_PreservesReorderedImage(t *testing.T) {
	rootBlocks := []*lark.DocxBlock{
		{BlockID: "imR", BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "T"}},    // 0 重排：token 仍在本地 → 保留
		{BlockID: "imGone", BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "G"}}, // 1 真删：token 不在本地
	}
	pairedOps := []PairedOp{
		{Type: PairedOpDelete, RemoteIdx: 0},
		{Type: PairedOpDelete, RemoteIdx: 1},
	}
	localTokens := map[string]bool{"T": true}

	dels := collectDeletions(pairedOps, rootBlocks, nil, localTokens)
	assert.Equal(t, []string{"imGone"}, dels)
	assert.NotContains(t, dels, "imR", "token 仍在本地的图片（重排）绝不删除")
}

// TestPlanEntityMoves_ReorderedImage 图片重排（远程末尾、本地开头）→ 按 token 规划移到开头。
func TestPlanEntityMoves_ReorderedImage(t *testing.T) {
	const docID = "DOC"
	p1 := mkRemoteText("p1", docID, "para-ONE")
	p2 := mkRemoteText("p2", docID, "para-TWO")
	img := &lark.DocxBlock{BlockID: "im", ParentID: docID, BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "T"}}
	current := []*lark.DocxBlock{p1, p2, img} // 远程：图片在末尾
	blockMap := map[string]*lark.DocxBlock{"p1": p1, "p2": p2, "im": img}

	// 本地：图片在最前（已解析 token=T）
	local := &ConvertResult{TopBlocks: []*lark.DocxBlock{
		{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "T"}},
		mkLocalText("para-ONE"),
		mkLocalText("para-TWO"),
	}}

	moves := planEntityMoves(current, blockMap, local, docID)
	if assert.Len(t, moves, 1, "重排图片应规划 1 次移动") {
		assert.Equal(t, "im", moves[0].remoteID, "应按 token 匹配到远程图片块 ID")
		assert.Equal(t, docID, moves[0].anchorID, "图片应移到文档开头")
	}
}

// TestPlanEntityMoves_FileViaViewInPlace view 包裹的 file 已就位 → 不规划移动（移动的是 view 根块）。
func TestPlanEntityMoves_FileViaViewInPlace(t *testing.T) {
	const docID = "DOC"
	innerFile := &lark.DocxBlock{BlockID: "inF", ParentID: "vw", BlockType: lark.DocxBlockTypeFile, File: &lark.DocxBlockFile{Token: "T"}}
	view := &lark.DocxBlock{BlockID: "vw", ParentID: docID, BlockType: lark.DocxBlockTypeView, Children: []string{"inF"}}
	current := []*lark.DocxBlock{view}
	blockMap := map[string]*lark.DocxBlock{"vw": view, "inF": innerFile}

	local := &ConvertResult{TopBlocks: []*lark.DocxBlock{
		{BlockType: lark.DocxBlockTypeFile, File: &lark.DocxBlockFile{Token: "T"}},
	}}
	assert.Nil(t, planEntityMoves(current, blockMap, local, docID), "view 包裹文件已就位不应规划移动")
}
