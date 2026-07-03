package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMirrorManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// 不存在时返回 (nil, nil)
	m, err := ReadMirrorManifest(dir)
	require.NoError(t, err)
	assert.Nil(t, m)

	src := "https://example.feishu.cn/wiki/space/7034567890"
	require.NoError(t, WriteMirrorManifest(dir, &MirrorManifest{Source: src, Follow: true, FollowDepth: 2}))

	m, err = ReadMirrorManifest(dir)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, src, m.Source)
	assert.True(t, m.Follow)
	assert.Equal(t, 2, m.FollowDepth)
}

// 旧版边车（只有 source 键）解析后 follow 字段为零值，向后兼容
func TestMirrorManifestLegacyYaml(t *testing.T) {
	dir := t.TempDir()
	legacy := "source: https://example.feishu.cn/wiki/space/7034567890\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, MirrorManifestFilename), []byte(legacy), 0o644))

	m, err := ReadMirrorManifest(dir)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "https://example.feishu.cn/wiki/space/7034567890", m.Source)
	assert.False(t, m.Follow)
	assert.Zero(t, m.FollowDepth)
}

// 未开 follow 时写出的边车不含 follow 键（omitempty），与旧版格式一致
func TestMirrorManifestOmitsFollowWhenOff(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WriteMirrorManifest(dir, &MirrorManifest{Source: "https://x.feishu.cn/wiki/space/1"}))
	data, err := os.ReadFile(filepath.Join(dir, MirrorManifestFilename))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "follow")
}

func TestGenerateMirrorClaudeMd(t *testing.T) {
	src := "https://example.feishu.cn/wiki/space/7034567890"
	content := GenerateMirrorClaudeMd("产品知识库", src, false)

	assert.True(t, strings.HasPrefix(content, "# 产品知识库（飞书文档镜像）\n"))
	assert.Contains(t, content, src)
	assert.Contains(t, content, "llms.txt")
	assert.Contains(t, content, "docs_map.md")
	assert.Contains(t, content, MirrorManifestFilename)
	assert.Contains(t, content, "larkdown mirror")
	// 未开 follow 不提 _refs
	assert.NotContains(t, content, "_refs")
	// 内容确定性：不含时间戳，重复生成零 diff
	assert.Equal(t, content, GenerateMirrorClaudeMd("产品知识库", src, false))

	followed := GenerateMirrorClaudeMd("产品知识库", src, true)
	assert.Contains(t, followed, "`_refs/`")
	assert.Contains(t, followed, "引用文档 (_refs)")
}

// writeDocWithSource 写一个带末尾 frontmatter 的镜像文档
func writeDocWithSource(t *testing.T, path, source string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	content := "# Doc\n\nbody\n" + GenerateFrontMatter(FrontMatter{Source: source})
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestPruneStaleMirrorFiles(t *testing.T) {
	// 回收站重定向到临时目录，避免污染真实环境
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmpHome, ".local", "share"))

	root := t.TempDir()

	keep := filepath.Join(root, "keep.md")
	writeDocWithSource(t, keep, "https://example.feishu.cn/wiki/KeepToken123")

	stale := filepath.Join(root, "stale.md")
	writeDocWithSource(t, stale, "https://example.feishu.cn/wiki/StaleToken456")

	// 子目录中的 stale 文档，清理后目录变空应被删除
	staleNested := filepath.Join(root, "sub", "nested.md")
	writeDocWithSource(t, staleNested, "https://example.feishu.cn/docx/StaleToken789")

	// 无 frontmatter 的 .md（CLAUDE.md、docs_map.md、用户文件）不动
	plain := filepath.Join(root, "CLAUDE.md")
	require.NoError(t, os.WriteFile(plain, []byte("# 说明\n"), 0o644))

	// 非 .md 文件不动
	asset := filepath.Join(root, "sub2", "image.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(asset), 0o755))
	require.NoError(t, os.WriteFile(asset, []byte("png"), 0o644))

	seen := map[string]bool{"KeepToken123": true}
	removed, err := PruneStaleMirrorFiles(root, func(token string) bool { return seen[token] })
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{stale, staleNested}, removed)
	assert.FileExists(t, keep)
	assert.FileExists(t, plain)
	assert.FileExists(t, asset)
	assert.NoFileExists(t, stale)
	assert.NoFileExists(t, staleNested)
	// 变空的子目录被删除，非空目录与根目录保留
	assert.NoDirExists(t, filepath.Join(root, "sub"))
	assert.DirExists(t, filepath.Join(root, "sub2"))
	assert.DirExists(t, root)
}

func TestPruneStaleMirrorFilesAllSeen(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmpHome, ".local", "share"))

	root := t.TempDir()
	doc := filepath.Join(root, "a.md")
	writeDocWithSource(t, doc, "https://example.feishu.cn/wiki/TokenA1")

	removed, err := PruneStaleMirrorFiles(root, func(string) bool { return true })
	require.NoError(t, err)
	assert.Empty(t, removed)
	assert.FileExists(t, doc)
}
