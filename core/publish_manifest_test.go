package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractMiaodaAppID(t *testing.T) {
	cases := map[string]string{
		"app_4k5jepcbjmv6m":                                                 "app_4k5jepcbjmv6m",
		"  app_4k5jepcbjmv6m  ":                                             "app_4k5jepcbjmv6m",
		"https://miaoda.feishu.cn/app/app_4k5jepcbjmv6m":                    "app_4k5jepcbjmv6m",
		"https://miaoda.feishu.cn/app/app_4k5jepcbjmv6m/":                   "app_4k5jepcbjmv6m",
		"https://miaoda.feishu.cn/app/app_4k5jepcbjmv6m?x=1":                "app_4k5jepcbjmv6m",
		"https://gcn35x0q7ppz.aiforce.cloud/app/app_4k8wv7z4tzs2x/":         "app_4k8wv7z4tzs2x",
		"https://gcn35x0q7ppz.aiforce.cloud/app/app_4k8wv7z4tzs2x/edit?x=1": "app_4k8wv7z4tzs2x",
		"": "",
	}
	for in, want := range cases {
		assert.Equalf(t, want, ExtractMiaodaAppID(in), "input=%q", in)
	}
}

func TestMiaodaManageURL(t *testing.T) {
	got := MiaodaManageURL("app_4k5jepcbjmv6m")
	assert.Equal(t, "https://miaoda.feishu.cn/app/app_4k5jepcbjmv6m", got)
	// 管理链接可被 ExtractMiaodaAppID 反解回原 app_id（与 --app-id 复用闭环）
	assert.Equal(t, "app_4k5jepcbjmv6m", ExtractMiaodaAppID(got))
}

func TestPublishManifest_RoundTrip(t *testing.T) {
	sp := NewStatePaths(t.TempDir())
	target := "/abs/path/to/dist"

	// 不存在时返回 nil
	m, err := ReadPublishManifest(sp, target)
	require.NoError(t, err)
	assert.Nil(t, m)

	want := &PublishManifest{MiaodaAppID: "app_x", MiaodaURL: "https://miaoda.feishu.cn/app/app_x", Name: "dist"}
	require.NoError(t, WritePublishManifest(sp, target, want))

	// 落在中心 store publish/<sha256>.yaml
	path := sp.PublishManifestFile(target)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr)
	assert.Equal(t, ".yaml", filepath.Ext(path))

	// 文件顶部带注释提示，带前缀独立字段记录后端状态，并冗余 target_path
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "# larkdown 发布记录")
	assert.Contains(t, string(raw), "miaoda_appid: app_x")
	assert.Contains(t, string(raw), "target_path: "+target)
	assert.NotContains(t, string(raw), "type:")

	got, err := ReadPublishManifest(sp, target)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.MiaodaAppID, got.MiaodaAppID)
	assert.Equal(t, want.MiaodaURL, got.MiaodaURL)
	assert.Equal(t, want.Name, got.Name)
	assert.Equal(t, target, got.TargetPath)
}

func TestPublishManifest_DistinctTargetsIsolated(t *testing.T) {
	sp := NewStatePaths(t.TempDir())

	require.NoError(t, WritePublishManifest(sp, "/a/report.html", &PublishManifest{MiaodaAppID: "app_a"}))
	require.NoError(t, WritePublishManifest(sp, "/b/report.html", &PublishManifest{MiaodaAppID: "app_b"}))

	gotA, err := ReadPublishManifest(sp, "/a/report.html")
	require.NoError(t, err)
	require.NotNil(t, gotA)
	assert.Equal(t, "app_a", gotA.MiaodaAppID)

	gotB, err := ReadPublishManifest(sp, "/b/report.html")
	require.NoError(t, err)
	require.NotNil(t, gotB)
	assert.Equal(t, "app_b", gotB.MiaodaAppID)
}

func TestBuildMiaodaTarball_SkipsManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644))
	// 模拟历史残留的旧边车文件（中心化后不再生成，但需确保不会被打进产物 tar）
	require.NoError(t, os.WriteFile(filepath.Join(dir, publishManifestName), []byte("stale"), 0o644))

	data, err := buildMiaodaTarball(dir)
	require.NoError(t, err)

	files := untarGz(t, data)
	assert.Contains(t, files, "index.html")
	assert.NotContains(t, files, publishManifestName)
}
