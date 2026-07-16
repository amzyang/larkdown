package main

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaskSecret(t *testing.T) {
	assert.Equal(t, "", maskSecret(""))
	assert.Equal(t, "****", maskSecret("short"))
	assert.Equal(t, "****", maskSecret("12345678")) // 恰好 8 位仍全遮
	assert.Equal(t, "****cdef", maskSecret("0123456789abcdef"))
}

func TestMaskConfigForDisplay(t *testing.T) {
	config := core.NewConfig("cli_app_id", "supersecretvalue")
	config.Feishu.UserAccessToken = "u-verylongusertoken"
	config.Feishu.RefreshToken = "r-verylongrefreshtoken"

	masked := maskConfigForDisplay(config)

	// app_id 不脱敏，三个敏感字段只留末 4 位
	assert.Equal(t, "cli_app_id", masked.Feishu.AppId)
	assert.Equal(t, "****alue", masked.Feishu.AppSecret)
	assert.Equal(t, "****oken", masked.Feishu.UserAccessToken)
	assert.Equal(t, "****oken", masked.Feishu.RefreshToken)

	// 原 config 不受影响（脱敏值绝不写回文件）
	assert.Equal(t, "supersecretvalue", config.Feishu.AppSecret)
	assert.Equal(t, "u-verylongusertoken", config.Feishu.UserAccessToken)
	assert.Equal(t, "r-verylongrefreshtoken", config.Feishu.RefreshToken)
}

// TestConfigGetSetValue 锁 config get/set 的键集合与值校验契约。
func TestConfigGetSetValue(t *testing.T) {
	config := core.NewConfig("cli_a", "secret_12345678")

	t.Run("get 已知键", func(t *testing.T) {
		v, err := configGetValue(config, "image_dir")
		require.NoError(t, err)
		assert.Equal(t, "static", v)
		v, err = configGetValue(config, "title_as_filename")
		require.NoError(t, err)
		assert.Equal(t, "true", v)
	})

	t.Run("get secret 脱敏", func(t *testing.T) {
		v, err := configGetValue(config, "app_secret")
		require.NoError(t, err)
		assert.Equal(t, "****5678", v)
	})

	t.Run("get 未知键", func(t *testing.T) {
		_, err := configGetValue(config, "bogus")
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Contains(t, ee.msg, "未知配置键")
	})

	t.Run("set 布尔键", func(t *testing.T) {
		require.NoError(t, configSetValue(config, "skip_img_download", "true"))
		assert.True(t, config.Output.SkipImgDownload)
		err := configSetValue(config, "skip_img_download", "bogus")
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Contains(t, ee.msg, "需要布尔值")
	})

	t.Run("set diff_style 校验 chroma 样式", func(t *testing.T) {
		require.NoError(t, configSetValue(config, "diff_style", "github"))
		assert.Equal(t, "github", config.Output.DiffStyle)
		err := configSetValue(config, "diff_style", "not-a-style")
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Contains(t, ee.msg, "chroma 样式名")
	})

	t.Run("set 未知键", func(t *testing.T) {
		err := configSetValue(config, "bogus", "x")
		var ee *exitError
		require.ErrorAs(t, err, &ee)
		assert.Contains(t, ee.msg, "未知配置键")
	})
}

// TestConfigGetMissingFile 锁 config get 对缺失配置文件的处理：
// 友好 exitError（用户可自助，不上报 Sentry），而非裸 PathError。
func TestConfigGetMissingFile(t *testing.T) {
	t.Setenv("LARKDOWN_CONFIG", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("DO_NOT_TRACK", "1")
	root := newRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"config", "get"})

	err := root.Execute()

	var ee *exitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.code)
	assert.Contains(t, ee.msg, "配置文件不存在")
	assert.Contains(t, ee.msg, "larkdown config init")
}
