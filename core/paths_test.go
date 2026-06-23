package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublishKey(t *testing.T) {
	// 稳定：同输入同输出
	assert.Equal(t, publishKey("/a/b/c"), publishKey("/a/b/c"))

	// filepath.Clean 归一：等价路径产生同一 key
	assert.Equal(t, publishKey("/a/b/c"), publishKey("/a/./b/c"))
	assert.Equal(t, publishKey("/a/b/c"), publishKey("/a/b/c/"))
	assert.Equal(t, publishKey("/a/b/c"), publishKey("/a/b/d/../c"))

	// 不同路径产生不同 key
	assert.NotEqual(t, publishKey("/a/b/c"), publishKey("/a/b/d"))

	// sha256 全量 hex（64 字符），消除路径特殊字符问题
	key := publishKey("/some/path with 空格/emoji😀")
	assert.Len(t, key, 64)
	assert.Regexp(t, "^[0-9a-f]{64}$", key)
}

func TestStatePathsLayout(t *testing.T) {
	base := t.TempDir()
	sp := NewStatePaths(base)

	assert.Equal(t, filepath.Join(base, "config.json"), sp.ConfigFile())
	assert.Equal(t, filepath.Join(base, "boards"), sp.BoardsDir())
	assert.Equal(t, filepath.Join(base, "publish"), sp.PublishDir())

	// board manifest 文件名即 document_id，落在 boards/ 下，.yaml 扩展名
	bf := sp.BoardManifestFile("doc123")
	assert.Equal(t, filepath.Join(base, "boards", "doc123.yaml"), bf)
	assert.Equal(t, ".yaml", filepath.Ext(bf))

	// publish manifest 文件名为路径 sha256，落在 publish/ 下
	pf := sp.PublishManifestFile("/x/dist")
	assert.Equal(t, filepath.Join(base, "publish", publishKey("/x/dist")+".yaml"), pf)
	assert.True(t, strings.HasPrefix(pf, sp.PublishDir()))
}

func TestCachePathsLayout(t *testing.T) {
	base := t.TempDir()
	cp := NewCachePaths(base)

	assert.Equal(t, filepath.Join(base, "images"), cp.ImagesDir())
	assert.Equal(t, filepath.Join(base, "files"), cp.FilesDir())
	assert.Equal(t, filepath.Join(base, "whiteboards"), cp.WhiteboardsDir())
}

func TestDefaultPathsUseAppDirName(t *testing.T) {
	// 默认路径包含 feishu2md 目录名（零迁移兼容约定）
	sp, err := DefaultStatePaths()
	assert.NoError(t, err)
	assert.Contains(t, sp.ConfigFile(), appDirName)

	cp, err := DefaultCachePaths()
	assert.NoError(t, err)
	assert.Contains(t, cp.ImagesDir(), appDirName)
}
