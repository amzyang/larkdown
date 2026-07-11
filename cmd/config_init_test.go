package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkdown/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConfigInitOptions(t *testing.T) {
	assert.NoError(t, validateConfigInitOptions(configInitOptions{}))
	assert.NoError(t, validateConfigInitOptions(configInitOptions{noWait: true}))
	assert.NoError(t, validateConfigInitOptions(configInitOptions{deviceCode: "dc"}))
	// --no-wait 与 --device-code 互斥
	assert.Error(t, validateConfigInitOptions(configInitOptions{noWait: true, deviceCode: "dc"}))
}

func TestCheckExistingCredentials(t *testing.T) {
	// 空白配置无需 --force
	assert.NoError(t, checkExistingCredentials(core.NewConfig("", ""), false))

	// 已有凭证且无 --force → 报错提示 --force，且不泄露完整 appId
	err := checkExistingCredentials(core.NewConfig("cli_old12345", "sec"), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
	assert.Contains(t, err.Error(), "auth login")
	assert.NotContains(t, err.Error(), "cli_old12345")

	// --force 放行
	assert.NoError(t, checkExistingCredentials(core.NewConfig("cli_old12345", "sec"), true))
}

func TestEmitAppRegistrationJSON(t *testing.T) {
	code := &core.AppRegistrationCode{
		DeviceCode:              "the-dc",
		UserCode:                "WDJB-MJHT",
		VerificationURI:         "https://example.com/page/cli",
		VerificationURIComplete: "https://example.com/page/cli?user_code=WDJB-MJHT",
		ExpiresIn:               300,
		Interval:                5,
	}
	var buf bytes.Buffer
	emitAppRegistration(&buf, code, true, true, false)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "app_registration", got["event"])
	assert.Equal(t, "the-dc", got["device_code"])
	assert.Equal(t, "WDJB-MJHT", got["user_code"])
	assert.Equal(t, "https://example.com/page/cli?user_code=WDJB-MJHT", got["verification_uri_complete"])
	assert.Equal(t, float64(300), got["expires_in"])
	assert.Equal(t, float64(5), got["interval"])
}

func TestEmitAppRegistrationTextShowsResumeHint(t *testing.T) {
	code := &core.AppRegistrationCode{
		DeviceCode:              "the-dc",
		UserCode:                "UC",
		VerificationURI:         "https://example.com/page/cli",
		VerificationURIComplete: "https://example.com/page/cli?user_code=UC",
	}

	var withHint bytes.Buffer
	emitAppRegistration(&withHint, code, false, true, false)
	assert.Contains(t, withHint.String(), "larkdown config init --device-code the-dc")
	assert.NotContains(t, withHint.String(), "--force")

	// 第二段是独立进程，不继承第一段的覆盖确认 → 恢复命令需带上 --force
	var withForce bytes.Buffer
	emitAppRegistration(&withForce, code, false, true, true)
	assert.Contains(t, withForce.String(), "larkdown config init --device-code the-dc --force")

	// 阻塞模式（showResumeHint=false）不打印恢复命令
	var noHint bytes.Buffer
	emitAppRegistration(&noHint, code, false, false, false)
	assert.NotContains(t, noHint.String(), "--device-code")
}

func TestSaveAppCredentialsWritesAndClearsTokens(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := core.NewConfig("cli_old12345", "sec_old")
	config.Feishu.UserAccessToken = "u-old"
	config.Feishu.RefreshToken = "r-old"
	config.Feishu.TokenExpireTime = 123
	config.Feishu.RefreshTokenExpireTime = 456
	config.Output.ImageDir = "assets" // 自定义 Output 需原样保留

	var buf bytes.Buffer
	err := saveAppCredentials(&buf, config, configPath, &core.AppCredentials{
		AppId:       "cli_new",
		AppSecret:   "sec_new_123456",
		OpenID:      "ou_x",
		TenantBrand: "feishu",
	}, false)
	require.NoError(t, err)

	onDisk, err := core.ReadConfigFromFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, "cli_new", onDisk.Feishu.AppId)
	assert.Equal(t, "sec_new_123456", onDisk.Feishu.AppSecret)
	// 旧 token 属旧应用，必须清空
	assert.Empty(t, onDisk.Feishu.UserAccessToken)
	assert.Empty(t, onDisk.Feishu.RefreshToken)
	assert.Zero(t, onDisk.Feishu.TokenExpireTime)
	assert.Zero(t, onDisk.Feishu.RefreshTokenExpireTime)
	assert.Equal(t, "assets", onDisk.Output.ImageDir)

	out := buf.String()
	assert.Contains(t, out, "应用创建成功")
	assert.Contains(t, out, "larkdown auth login")
	assert.NotContains(t, out, "sec_new_123456") // stdout 不泄露明文 secret
}

func TestSaveAppCredentialsJSON(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := core.NewConfig("", "")

	var buf bytes.Buffer
	err := saveAppCredentials(&buf, config, configPath, &core.AppCredentials{
		AppId:       "cli_new",
		AppSecret:   "sec_new_123456",
		OpenID:      "ou_x",
		TenantBrand: "feishu",
	}, true)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "app_registered", got["event"])
	assert.Equal(t, "cli_new", got["app_id"])
	assert.Equal(t, "ou_x", got["open_id"])
	assert.Equal(t, "feishu", got["tenant_brand"])
	assert.NotContains(t, buf.String(), "sec_new_123456")
}
