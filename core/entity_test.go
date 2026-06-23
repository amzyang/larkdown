package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

func TestEntityRefFromBlock(t *testing.T) {
	img := &lark.DocxBlock{
		BlockID:   "imgblk",
		BlockType: lark.DocxBlockTypeImage,
		Image:     &lark.DocxBlockImage{Token: "IMG_TOK"},
	}
	file := &lark.DocxBlock{
		BlockID:   "fileblk",
		BlockType: lark.DocxBlockTypeFile,
		File:      &lark.DocxBlockFile{Token: "FILE_TOK"},
	}
	// View(33) 包裹 file：内层 children[0] 才是真实 file 块
	innerFile := &lark.DocxBlock{
		BlockID:   "innerfile",
		BlockType: lark.DocxBlockTypeFile,
		File:      &lark.DocxBlockFile{Token: "WRAPPED_TOK"},
	}
	view := &lark.DocxBlock{
		BlockID:   "viewblk",
		BlockType: lark.DocxBlockTypeView,
		Children:  []string{"innerfile"},
	}
	blockMap := map[string]*lark.DocxBlock{"innerfile": innerFile}

	t.Run("image direct", func(t *testing.T) {
		ref, ok := entityRefFromBlock(img, nil)
		assert.True(t, ok)
		assert.Equal(t, "IMG_TOK", ref.token)
		assert.Equal(t, "imgblk", ref.innerBlockID)
		assert.False(t, ref.isFile)
	})
	t.Run("file direct", func(t *testing.T) {
		ref, ok := entityRefFromBlock(file, nil)
		assert.True(t, ok)
		assert.Equal(t, "FILE_TOK", ref.token)
		assert.Equal(t, "fileblk", ref.innerBlockID)
		assert.True(t, ref.isFile)
	})
	t.Run("view-wrapped file", func(t *testing.T) {
		ref, ok := entityRefFromBlock(view, blockMap)
		assert.True(t, ok)
		assert.Equal(t, "WRAPPED_TOK", ref.token)
		assert.Equal(t, "innerfile", ref.innerBlockID) // 内层真实块，非 view
		assert.True(t, ref.isFile)
	})
	t.Run("non-entity", func(t *testing.T) {
		_, ok := entityRefFromBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeText}, nil)
		assert.False(t, ok)
	})
	t.Run("image without token", func(t *testing.T) {
		_, ok := entityRefFromBlock(&lark.DocxBlock{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{}}, nil)
		assert.False(t, ok)
	})
}

func TestMatchTokenByPrefix(t *testing.T) {
	tokens := map[string]bool{
		"IMG_BOXabc":  true, // token 自身含下划线
		"IMG_BOXabcd": true, // 与上者互为前缀，取最长
		"FILE_xyz":    true,
	}
	assert.Equal(t, "IMG_BOXabc", matchTokenByPrefix("IMG_BOXabc_photo.png", tokens))
	assert.Equal(t, "IMG_BOXabcd", matchTokenByPrefix("IMG_BOXabcd_report.png", tokens))
	assert.Equal(t, "FILE_xyz", matchTokenByPrefix("FILE_xyz_数据.pdf", tokens))
	assert.Equal(t, "", matchTokenByPrefix("unrelated.png", tokens))
	assert.Equal(t, "", matchTokenByPrefix("IMG_BOXabc.png", tokens)) // 无 "_" 分隔不算命中
}

func TestResolveEntityTokens(t *testing.T) {
	imgBlock := &lark.DocxBlock{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{}}
	fileBlock := &lark.DocxBlock{BlockType: lark.DocxBlockTypeFile, File: &lark.DocxBlockFile{}}
	newImg := &lark.DocxBlock{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{}}
	result := &ConvertResult{
		TopBlocks:    []*lark.DocxBlock{imgBlock, fileBlock, newImg},
		ImageIndices: []int{0, 2},
		ImagePaths:   []string{"static/IMG_TOK_a.png", "static/handmade.png"},
		FileIndices:  []int{1},
		FilePaths:    []string{"static/FILE_TOK_b.pdf"},
	}
	remoteTokens := map[string]bool{"IMG_TOK": true, "FILE_TOK": true}

	resolveEntityTokens(result, remoteTokens)

	assert.Equal(t, "IMG_TOK", imgBlock.Image.Token, "round-trip 图片应解析出 token")
	assert.Equal(t, "FILE_TOK", fileBlock.File.Token, "round-trip 文件应解析出 token")
	assert.Equal(t, "", newImg.Image.Token, "手动添加的新图片无 token 前缀，应保持为空（视为新增）")

	assert.True(t, isRoundTripEntity(imgBlock))
	assert.True(t, isRoundTripEntity(fileBlock))
	assert.False(t, isRoundTripEntity(newImg))
}

func TestLocalEntityTokenSet(t *testing.T) {
	result := &ConvertResult{
		TopBlocks: []*lark.DocxBlock{
			{BlockType: lark.DocxBlockTypeImage, Image: &lark.DocxBlockImage{Token: "T1"}},
			{BlockType: lark.DocxBlockTypeFile, File: &lark.DocxBlockFile{Token: "T2"}},
			{BlockType: lark.DocxBlockTypeBoard, Board: &lark.DocxBlockBoard{Token: "B1"}},
			{BlockType: lark.DocxBlockTypeText}, // 非实体
		},
	}
	set := localEntityTokenSet(result)
	assert.Equal(t, map[string]bool{"T1": true, "T2": true, "B1": true}, set)
}

func TestEntityContentChanged(t *testing.T) {
	dir := t.TempDir()
	cache := &ImageCache{cacheDir: filepath.Join(dir, "images"), filesCacheDir: filepath.Join(dir, "files")}

	// 原始内容写入缓存
	assert.NoError(t, cache.Put("IMG_TOK", []byte("original-bytes"), "a.png"))

	// 本地文件：未变
	unchanged := filepath.Join(dir, "IMG_TOK_a.png")
	assert.NoError(t, os.WriteFile(unchanged, []byte("original-bytes"), 0o644))
	// 本地文件：已编辑
	edited := filepath.Join(dir, "IMG_TOK_edited.png")
	assert.NoError(t, os.WriteFile(edited, []byte("EDITED-bytes"), 0o644))

	ref := entityRef{token: "IMG_TOK", isFile: false}
	// 绝对路径：mdDir 被忽略
	assert.False(t, entityContentChanged(cache, ref, unchanged, ""), "内容一致应判未变")
	assert.True(t, entityContentChanged(cache, ref, edited, ""), "内容不同应判已变")

	// 缓存缺失 → 默认未变（best-effort）
	missRef := entityRef{token: "NO_CACHE", isFile: false}
	assert.False(t, entityContentChanged(cache, missRef, edited, ""))
}

// TestEntityContentChanged_RelativePath 相对资源路径须按 mdDir 解析（与上传读字节口径一致），
// 而非按进程 cwd——否则下载产物（相对链接）在异目录上传时编辑检测会静默失效。
func TestEntityContentChanged_RelativePath(t *testing.T) {
	dir := t.TempDir()
	cache := &ImageCache{cacheDir: filepath.Join(dir, "images"), filesCacheDir: filepath.Join(dir, "files")}
	assert.NoError(t, cache.Put("IMG_TOK", []byte("original-bytes"), "a.png"))

	// 资源放在 mdDir 子目录，markdown 里是相对链接 docname/IMG_TOK_edited.png
	mdDir := filepath.Join(dir, "md")
	assetDir := filepath.Join(mdDir, "docname")
	assert.NoError(t, os.MkdirAll(assetDir, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(assetDir, "IMG_TOK_edited.png"), []byte("EDITED-bytes"), 0o644))
	assert.NoError(t, os.WriteFile(filepath.Join(assetDir, "IMG_TOK_same.png"), []byte("original-bytes"), 0o644))

	ref := entityRef{token: "IMG_TOK", isFile: false}
	assert.True(t, entityContentChanged(cache, ref, "docname/IMG_TOK_edited.png", mdDir), "相对路径编辑应被检出")
	assert.False(t, entityContentChanged(cache, ref, "docname/IMG_TOK_same.png", mdDir), "相对路径内容一致应判未变")
}

// TestEntityContentChanged_RemoteURL 远程 URL 无本地编辑语义、无法 md5 比对 → 永远判未变（不重传）。
func TestEntityContentChanged_RemoteURL(t *testing.T) {
	dir := t.TempDir()
	cache := &ImageCache{cacheDir: filepath.Join(dir, "images"), filesCacheDir: filepath.Join(dir, "files")}
	assert.NoError(t, cache.Put("IMG_TOK", []byte("original-bytes"), "a.png"))

	ref := entityRef{token: "IMG_TOK", isFile: false}
	assert.False(t, entityContentChanged(cache, ref, "https://example.com/a.png", ""), "远程 URL 应判未变")
	assert.False(t, entityContentChanged(cache, ref, "http://example.com/a.png", ""), "远程 URL 应判未变")
}
