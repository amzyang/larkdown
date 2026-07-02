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

func TestValidateLoginOptions(t *testing.T) {
	assert.NoError(t, validateLoginOptions(loginOptions{}))
	assert.NoError(t, validateLoginOptions(loginOptions{noWait: true}))
	assert.NoError(t, validateLoginOptions(loginOptions{deviceCode: "dc"}))
	// --no-wait 与 --device-code 互斥
	assert.Error(t, validateLoginOptions(loginOptions{noWait: true, deviceCode: "dc"}))
}

func TestEmitDeviceAuthorizationJSON(t *testing.T) {
	dc := &core.DeviceCodeResult{
		DeviceCode:              "the-dc",
		UserCode:                "WDJB-MJHT",
		VerificationURI:         "https://example.com/device",
		VerificationURIComplete: "https://example.com/device?code=WDJB-MJHT",
		ExpiresIn:               240,
		Interval:                5,
	}
	var buf bytes.Buffer
	emitDeviceAuthorization(&buf, dc, true, true)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "device_authorization", got["event"])
	assert.Equal(t, "the-dc", got["device_code"])
	assert.Equal(t, "WDJB-MJHT", got["user_code"])
	assert.Equal(t, "https://example.com/device?code=WDJB-MJHT", got["verification_uri_complete"])
}

func TestEmitDeviceAuthorizationTextShowsResumeHint(t *testing.T) {
	dc := &core.DeviceCodeResult{
		DeviceCode:              "the-dc",
		UserCode:                "UC",
		VerificationURI:         "https://example.com/device",
		VerificationURIComplete: "https://example.com/device?code=UC",
	}

	var withHint bytes.Buffer
	emitDeviceAuthorization(&withHint, dc, false, true)
	assert.Contains(t, withHint.String(), "larkdown auth login --device-code the-dc")

	// 阻塞模式（showResumeHint=false）不打印恢复命令
	var noHint bytes.Buffer
	emitDeviceAuthorization(&noHint, dc, false, false)
	assert.NotContains(t, noHint.String(), "--device-code")
}

func TestSaveTokenWritesFieldsAndText(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := core.NewConfig("app-id", "app-secret")

	var buf bytes.Buffer
	err := saveToken(&buf, config, configPath, &core.TokenResult{
		UserAccessToken:  "u-tok",
		RefreshToken:     "r-tok",
		ExpiresIn:        7200,
		RefreshExpiresIn: 604800,
	}, false)
	require.NoError(t, err)

	assert.Equal(t, "u-tok", config.Feishu.UserAccessToken)
	assert.Equal(t, "r-tok", config.Feishu.RefreshToken)
	assert.Greater(t, config.Feishu.RefreshTokenExpireTime, int64(0))
	assert.Contains(t, buf.String(), "登录成功")

	onDisk, err := core.ReadConfigFromFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, "u-tok", onDisk.Feishu.UserAccessToken)
	assert.Equal(t, config.Feishu.RefreshTokenExpireTime, onDisk.Feishu.RefreshTokenExpireTime)
}

func TestSaveTokenJSON(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := core.NewConfig("app-id", "app-secret")

	var buf bytes.Buffer
	err := saveToken(&buf, config, configPath, &core.TokenResult{
		UserAccessToken:  "u-tok",
		RefreshToken:     "r-tok",
		ExpiresIn:        7200,
		RefreshExpiresIn: 604800,
	}, true)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "authorized", got["event"])
	assert.Greater(t, got["token_expire_time"].(float64), float64(0))
	assert.Greater(t, got["refresh_token_expire_time"].(float64), float64(0))
}
