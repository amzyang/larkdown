package core

import (
	"os"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

func TestCanonicalBoardSourceHash(t *testing.T) {
	base := "@startuml\nA -> B\n@enduml"

	// 行尾空白、首尾空行不影响 hash（归一化）
	assert.Equal(t, canonicalBoardSourceHash(base), canonicalBoardSourceHash("@startuml  \nA -> B\t\n@enduml"))
	assert.Equal(t, canonicalBoardSourceHash(base), canonicalBoardSourceHash("\n\n@startuml\nA -> B\n@enduml\n\n"))

	// 内容不同则 hash 不同
	assert.NotEqual(t, canonicalBoardSourceHash(base), canonicalBoardSourceHash("@startuml\nA -> C\n@enduml"))
}

func TestBoardManifestReadWriteRoundTrip(t *testing.T) {
	sp := NewStatePaths(t.TempDir())

	// 不存在 → (nil, nil)
	m, err := ReadBoardManifest(sp, "doc123")
	assert.NoError(t, err)
	assert.Nil(t, m)

	want := &BoardManifest{
		DocumentID: "doc123",
		Boards:     []BoardMapping{{SourceHash: "abc", Token: "WB_tok"}},
	}
	assert.NoError(t, WriteBoardManifest(sp, "doc123", want))

	// 落在中心 store boards/<document_id>.yaml，且带用途说明注释头
	raw, err := os.ReadFile(sp.BoardManifestFile("doc123"))
	assert.NoError(t, err)
	assert.Contains(t, string(raw), "# larkdown 画板映射记录")

	got, err := ReadBoardManifest(sp, "doc123")
	assert.NoError(t, err)
	assert.Equal(t, want, got)

	// 不同 document_id 互不干扰
	other, err := ReadBoardManifest(sp, "doc-other")
	assert.NoError(t, err)
	assert.Nil(t, other)
}

func TestBoardManifestLookupToken(t *testing.T) {
	m := &BoardManifest{DocumentID: "doc1", Boards: []BoardMapping{{SourceHash: "h1", Token: "t1"}}}

	assert.Equal(t, "t1", m.lookupToken("doc1", "h1"))
	assert.Equal(t, "", m.lookupToken("doc1", "h-miss"))                 // hash 不匹配
	assert.Equal(t, "", m.lookupToken("doc-other", "h1"))                // document 不匹配
	assert.Equal(t, "", (*BoardManifest)(nil).lookupToken("doc1", "h1")) // nil receiver
}

func TestBoardManifestUpsert(t *testing.T) {
	m := &BoardManifest{}

	m.upsert("doc1", "h1", "t1")
	assert.Equal(t, "doc1", m.DocumentID)
	assert.Equal(t, "t1", m.lookupToken("doc1", "h1"))

	// 同 token 更新 hash（源改动复用同一画板）
	m.upsert("doc1", "h1-new", "t1")
	assert.Len(t, m.Boards, 1)
	assert.Equal(t, "t1", m.lookupToken("doc1", "h1-new"))

	// 同 hash 不同 token → 追加（重复源画板各自保留独立 token）
	m.upsert("doc1", "h1-new", "t1-dup")
	assert.Len(t, m.Boards, 2)

	// 新 hash 追加
	m.upsert("doc1", "h2", "t2")
	assert.Len(t, m.Boards, 3)

	// document 变更 → 清空旧映射
	m.upsert("doc2", "h3", "t3")
	assert.Equal(t, "doc2", m.DocumentID)
	assert.Len(t, m.Boards, 1)
	assert.Equal(t, "t3", m.lookupToken("doc2", "h3"))
}

// boardResult 构造一个含单个无 token plantuml board 的 ConvertResult。
func boardResult(code string) *ConvertResult {
	return &ConvertResult{
		TopBlocks:    []*lark.DocxBlock{{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{}}},
		BoardIndices: []int{0},
		BoardCodes:   []string{code},
	}
}

func TestApplyBoardMappings(t *testing.T) {
	code := "@startuml\nA -> B\n@enduml"
	hash := canonicalBoardSourceHash(code)
	manifest := &BoardManifest{DocumentID: "doc1", Boards: []BoardMapping{{SourceHash: hash, Token: "WB_tok"}}}
	remote := map[string]bool{"WB_tok": true}

	// 命中且 token 仍在远程 → 写回，签名从 board:plantuml:* 变为 board:<token>
	result := boardResult(code)
	before := SignatureFromLocalEntry(result, 0)
	assert.Equal(t, 1, applyBoardMappings(result, "doc1", manifest, remote))
	assert.Equal(t, "WB_tok", result.TopBlocks[0].Board.Token)
	after := SignatureFromLocalEntry(result, 0)
	assert.Contains(t, string(before), "board:plantuml:")
	assert.Equal(t, BlockSignature("board:WB_tok"), after)

	// token 不在远程集合（陈旧映射）→ 不写回，仍走重建
	stale := boardResult(code)
	assert.Equal(t, 0, applyBoardMappings(stale, "doc1", manifest, map[string]bool{}))
	assert.Equal(t, "", stale.TopBlocks[0].Board.Token)

	// 源已变（hash 不命中）→ 不写回
	changed := boardResult("@startuml\nA -> C\n@enduml")
	assert.Equal(t, 0, applyBoardMappings(changed, "doc1", manifest, remote))
	assert.Equal(t, "", changed.TopBlocks[0].Board.Token)

	// manifest 为 nil → 0
	assert.Equal(t, 0, applyBoardMappings(boardResult(code), "doc1", nil, remote))

	// 重复源画板：同 hash 两条映射（不同 token）→ 两个本地画板各取一个独立 token
	dupManifest := &BoardManifest{DocumentID: "doc1", Boards: []BoardMapping{
		{SourceHash: hash, Token: "WB_a"},
		{SourceHash: hash, Token: "WB_b"},
	}}
	dupRemote := map[string]bool{"WB_a": true, "WB_b": true}
	dup := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{}},
			{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{}},
		},
		BoardIndices: []int{0, 1},
		BoardCodes:   []string{code, code},
	}
	assert.Equal(t, 2, applyBoardMappings(dup, "doc1", dupManifest, dupRemote))
	tok0 := dup.TopBlocks[0].Board.Token
	tok1 := dup.TopBlocks[1].Board.Token
	assert.NotEqual(t, tok0, tok1, "两个重复源画板应取到不同 token")
	assert.ElementsMatch(t, []string{"WB_a", "WB_b"}, []string{tok0, tok1})
}
