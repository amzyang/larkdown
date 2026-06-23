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

func TestPublishManifest_RoundTripDir(t *testing.T) {
	dir := t.TempDir()

	// 不存在时返回 nil
	m, err := ReadPublishManifest(dir)
	require.NoError(t, err)
	assert.Nil(t, m)

	want := &PublishManifest{MiaodaAppID: "app_x", MiaodaURL: "https://miaoda.feishu.cn/app/app_x", Name: "dist"}
	require.NoError(t, WritePublishManifest(dir, want))

	// 落在目录根，且为 .yaml 边车文件
	path := filepath.Join(dir, publishManifestName)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr)
	assert.Equal(t, ".yaml", filepath.Ext(publishManifestName))

	// 文件顶部带注释提示，并以带前缀的独立字段记录后端状态
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "# larkdown 发布记录")
	assert.Contains(t, string(raw), "miaoda_appid: app_x")
	assert.NotContains(t, string(raw), "type:")

	got, err := ReadPublishManifest(dir)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.MiaodaAppID, got.MiaodaAppID)
	assert.Equal(t, want.MiaodaURL, got.MiaodaURL)
	assert.Equal(t, want.Name, got.Name)
}

func TestPublishManifest_SingleFileSidecar(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "report.html")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	require.NoError(t, WritePublishManifest(file, &PublishManifest{MiaodaAppID: "app_y"}))

	// 边车文件落在文件旁
	_, statErr := os.Stat(file + publishManifestName)
	require.NoError(t, statErr)

	got, err := ReadPublishManifest(file)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "app_y", got.MiaodaAppID)
}

func TestBuildMiaodaTarball_SkipsManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o644))
	require.NoError(t, WritePublishManifest(dir, &PublishManifest{MiaodaAppID: "app_z"}))

	data, err := buildMiaodaTarball(dir)
	require.NoError(t, err)

	files := untarGz(t, data)
	assert.Contains(t, files, "index.html")
	assert.NotContains(t, files, publishManifestName)
}
