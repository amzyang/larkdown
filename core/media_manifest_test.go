package core

import (
	"os"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

// imageResult 构造含若干无 token image 块的 ConvertResult（按 paths 顺序）。
func imageResult(paths ...string) *ConvertResult {
	r := &ConvertResult{}
	for _, p := range paths {
		idx := len(r.TopBlocks)
		r.TopBlocks = append(r.TopBlocks, &lark.DocxBlock{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{}})
		r.ImageIndices = append(r.ImageIndices, idx)
		r.ImagePaths = append(r.ImagePaths, p)
	}
	return r
}

func TestMediaManifestReadWriteRoundTrip(t *testing.T) {
	sp := NewStatePaths(t.TempDir())

	// 不存在 → (nil, nil)
	m, err := ReadMediaManifest(sp, "doc123")
	assert.NoError(t, err)
	assert.Nil(t, m)

	want := &MediaManifest{
		DocumentID: "doc123",
		Entries:    []MediaMapping{{Path: "clock.png", Token: "img_tok", MD5: "abc", IsFile: false}},
	}
	assert.NoError(t, WriteMediaManifest(sp, "doc123", want))

	// 落在中心 store media/<document_id>.yaml，且带用途说明注释头
	raw, err := os.ReadFile(sp.MediaManifestFile("doc123"))
	assert.NoError(t, err)
	assert.Contains(t, string(raw), "# larkdown 媒体映射记录")

	got, err := ReadMediaManifest(sp, "doc123")
	assert.NoError(t, err)
	assert.Equal(t, want, got)

	// 不同 document_id 互不干扰
	other, err := ReadMediaManifest(sp, "doc-other")
	assert.NoError(t, err)
	assert.Nil(t, other)
}

func TestMediaManifestUpsert(t *testing.T) {
	m := &MediaManifest{}

	m.upsert("doc1", "a.png", "t1", "m1", false)
	assert.Equal(t, "doc1", m.DocumentID)
	assert.Len(t, m.Entries, 1)

	// 同 token 更新 path/md5（内容变更后 replace 复用记录、刷新基准）
	m.upsert("doc1", "a.png", "t1", "m2", false)
	assert.Len(t, m.Entries, 1)
	assert.Equal(t, "m2", m.Entries[0].MD5)

	// 同 path 不同 token → 追加（同一路径多次引用各保留独立 token）
	m.upsert("doc1", "a.png", "t2", "m3", false)
	assert.Len(t, m.Entries, 2)

	// 新 path 追加
	m.upsert("doc1", "b.pdf", "t3", "m4", true)
	assert.Len(t, m.Entries, 3)

	// document 变更 → 清空旧映射
	m.upsert("doc2", "c.png", "t9", "m9", false)
	assert.Equal(t, "doc2", m.DocumentID)
	assert.Len(t, m.Entries, 1)
}

func TestMediaManifestLookupUnusedToken(t *testing.T) {
	m := &MediaManifest{DocumentID: "doc1", Entries: []MediaMapping{
		{Path: "a.png", Token: "t1", MD5: "m1"},
		{Path: "a.png", Token: "t2", MD5: "m2"},
	}}
	remote := map[string]bool{"t1": true, "t2": true}
	used := map[string]bool{}

	// 同路径两次引用：依次取到两个独立 token + 各自基准 md5
	tok, md5 := m.lookupUnusedToken("doc1", "a.png", remote, used)
	assert.Equal(t, "t1", tok)
	assert.Equal(t, "m1", md5)
	used[tok] = true

	tok2, md52 := m.lookupUnusedToken("doc1", "a.png", remote, used)
	assert.Equal(t, "t2", tok2)
	assert.Equal(t, "m2", md52)
	used[tok2] = true

	// 都已复用 → ""
	tok3, _ := m.lookupUnusedToken("doc1", "a.png", remote, used)
	assert.Equal(t, "", tok3)

	// document 不匹配 → ""
	tokX, _ := m.lookupUnusedToken("doc-other", "a.png", remote, map[string]bool{})
	assert.Equal(t, "", tokX)

	// token 不在远程（陈旧）→ ""
	tokS, _ := m.lookupUnusedToken("doc1", "a.png", map[string]bool{}, map[string]bool{})
	assert.Equal(t, "", tokS)

	// nil receiver → ""
	tokN, _ := (*MediaManifest)(nil).lookupUnusedToken("doc1", "a.png", remote, map[string]bool{})
	assert.Equal(t, "", tokN)
}

func TestMediaContentChanged(t *testing.T) {
	assert.False(t, mediaContentChanged("abc", "abc"))
	assert.True(t, mediaContentChanged("abc", "def"))
}

func TestApplyMediaPathMappings(t *testing.T) {
	manifest := &MediaManifest{DocumentID: "doc1", Entries: []MediaMapping{
		{Path: "clock.png", Token: "img_tok", MD5: "md5a"},
	}}
	remote := map[string]bool{"img_tok": true}

	// 命中且 token 仍在远程 → 写回 + 返回基准；签名从 media:new:* 变为 media:<token>
	result := imageResult("clock.png")
	before := SignatureFromLocalEntry(result, 0)
	n, baseline := applyMediaPathMappings(result, "doc1", manifest, remote)
	assert.Equal(t, 1, n)
	assert.Equal(t, "img_tok", result.TopBlocks[0].Image.Token)
	assert.Equal(t, "md5a", baseline["img_tok"])
	assert.Contains(t, string(before), "media:new:")
	assert.Equal(t, BlockSignature("media:img_tok"), SignatureFromLocalEntry(result, 0))

	// token 不在远程集合（陈旧映射）→ 不写回
	stale := imageResult("clock.png")
	n, _ = applyMediaPathMappings(stale, "doc1", manifest, map[string]bool{})
	assert.Equal(t, 0, n)
	assert.Equal(t, "", stale.TopBlocks[0].Image.Token)

	// 路径不匹配（图片改名）→ 不写回
	renamed := imageResult("clock2.png")
	n, _ = applyMediaPathMappings(renamed, "doc1", manifest, remote)
	assert.Equal(t, 0, n)
	assert.Equal(t, "", renamed.TopBlocks[0].Image.Token)

	// 已有 token（文件名前缀机制已解析）→ 不覆盖（前缀优先）
	prefixed := imageResult("clock.png")
	prefixed.TopBlocks[0].Image.Token = "existing"
	n, _ = applyMediaPathMappings(prefixed, "doc1", manifest, remote)
	assert.Equal(t, 0, n)
	assert.Equal(t, "existing", prefixed.TopBlocks[0].Image.Token)

	// 远程 URL → 跳过（无本地编辑语义）
	urlManifest := &MediaManifest{DocumentID: "doc1", Entries: []MediaMapping{
		{Path: "https://example.com/clock.png", Token: "img_tok", MD5: "x"},
	}}
	remoteURL := imageResult("https://example.com/clock.png")
	n, _ = applyMediaPathMappings(remoteURL, "doc1", urlManifest, remote)
	assert.Equal(t, 0, n)

	// manifest 为 nil → 0
	n, _ = applyMediaPathMappings(imageResult("clock.png"), "doc1", nil, remote)
	assert.Equal(t, 0, n)

	// 同一路径引用两次：manifest 两条同 path 不同 token → 两个本地块各取独立 token
	dupManifest := &MediaManifest{DocumentID: "doc1", Entries: []MediaMapping{
		{Path: "a.png", Token: "t_a", MD5: "m1"},
		{Path: "a.png", Token: "t_b", MD5: "m2"},
	}}
	dupRemote := map[string]bool{"t_a": true, "t_b": true}
	dup := imageResult("a.png", "a.png")
	n, _ = applyMediaPathMappings(dup, "doc1", dupManifest, dupRemote)
	assert.Equal(t, 2, n)
	tok0 := dup.TopBlocks[0].Image.Token
	tok1 := dup.TopBlocks[1].Image.Token
	assert.NotEqual(t, tok0, tok1, "两个重复引用应取到不同 token")
	assert.ElementsMatch(t, []string{"t_a", "t_b"}, []string{tok0, tok1})

	// 路径写法抖动：边车存 "./images/foo.png"，本地引用 "images/foo.png" → 归一化后命中（不再当新图重传）
	driftManifest := &MediaManifest{DocumentID: "doc1", Entries: []MediaMapping{
		{Path: "./images/foo.png", Token: "drift_tok", MD5: "dm"},
	}}
	drift := imageResult("images/foo.png")
	n, _ = applyMediaPathMappings(drift, "doc1", driftManifest, map[string]bool{"drift_tok": true})
	assert.Equal(t, 1, n, "路径写法抖动（./images/foo.png vs images/foo.png）应归一化后命中")
	assert.Equal(t, "drift_tok", drift.TopBlocks[0].Image.Token)
}

func TestNormalizeMediaPath(t *testing.T) {
	// 本地相对路径：消除 ./、重复分隔符、. 段
	assert.Equal(t, "a.png", normalizeMediaPath("./a.png"))
	assert.Equal(t, "images/a.png", normalizeMediaPath("images/a.png"))
	assert.Equal(t, "images/a.png", normalizeMediaPath("./images/a.png"))
	assert.Equal(t, "a/b.png", normalizeMediaPath("a//b.png"))
	assert.Equal(t, "b.png", normalizeMediaPath("a/../b.png"))
	// 远程 URL 原样返回（path.Clean 会折叠 https:// 的 //，必须跳过）
	assert.Equal(t, "https://example.com/a.png", normalizeMediaPath("https://example.com/a.png"))
	assert.Equal(t, "http://example.com//x/a.png", normalizeMediaPath("http://example.com//x/a.png"))
}

func TestApplyMediaPathMappingsFile(t *testing.T) {
	manifest := &MediaManifest{DocumentID: "doc1", Entries: []MediaMapping{
		{Path: "doc.pdf", Token: "file_tok", MD5: "fm", IsFile: true},
	}}
	result := &ConvertResult{
		TopBlocks:   []*lark.DocxBlock{{BlockType: lark.DocxBlockTypeFile, File: &lark.DocxBlockFile{}}},
		FileIndices: []int{0},
		FilePaths:   []string{"doc.pdf"},
	}
	n, baseline := applyMediaPathMappings(result, "doc1", manifest, map[string]bool{"file_tok": true})
	assert.Equal(t, 1, n)
	assert.Equal(t, "file_tok", result.TopBlocks[0].File.Token)
	assert.Equal(t, "fm", baseline["file_tok"])
	assert.Equal(t, BlockSignature("media:file_tok"), SignatureFromLocalEntry(result, 0))
}
