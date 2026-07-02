package main

import (
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/stretchr/testify/assert"
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
