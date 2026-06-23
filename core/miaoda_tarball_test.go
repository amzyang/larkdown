package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// untarGz 解开 tar.gz，返回 relPath -> content 映射，供断言使用。
func untarGz(t *testing.T, data []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		buf, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[hdr.Name] = string(buf)
	}
	return out
}

func TestBuildMiaodaTarball_Directory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644))

	data, err := buildMiaodaTarball(dir)
	require.NoError(t, err)

	files := untarGz(t, data)
	assert.Equal(t, "<h1>hi</h1>", files["index.html"])
	assert.Equal(t, "console.log(1)", files["assets/app.js"])
}

func TestBuildMiaodaTarball_MissingIndex(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "home.html"), []byte("x"), 0o644))

	_, err := buildMiaodaTarball(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index.html")
}

func TestBuildMiaodaTarball_SingleFileBecomesIndex(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.html")
	require.NoError(t, os.WriteFile(report, []byte("<p>report</p>"), 0o644))

	data, err := buildMiaodaTarball(report)
	require.NoError(t, err)

	files := untarGz(t, data)
	// 单文件无论原名，统一作为 index.html 入口
	assert.Equal(t, "<p>report</p>", files["index.html"])
	assert.NotContains(t, files, "report.html")
}

func TestBuildMiaodaTarball_SkipsGitAndNodeModules(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "x", "i.js"), []byte("y"), 0o644))

	data, err := buildMiaodaTarball(dir)
	require.NoError(t, err)

	files := untarGz(t, data)
	assert.Contains(t, files, "index.html")
	assert.NotContains(t, files, ".git/HEAD")
	assert.NotContains(t, files, "node_modules/x/i.js")
}
