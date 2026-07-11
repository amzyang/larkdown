package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type revokeCall struct {
	token string
	hint  string
}

// fakeRevoker 记录每次 revoke 调用，可选地令全部调用失败。
type fakeRevoker struct {
	calls []revokeCall
	err   error
}

func (f *fakeRevoker) RevokeToken(ctx context.Context, token, hint string) error {
	f.calls = append(f.calls, revokeCall{token: token, hint: hint})
	return f.err
}

func newLoggedInConfig(t *testing.T) (*Config, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := NewConfig("app-id", "app-secret")
	config.Feishu.UserAccessToken = "u-tok"
	config.Feishu.RefreshToken = "r-tok"
	config.Feishu.TokenExpireTime = time.Now().Unix() + 7200
	require.NoError(t, config.WriteConfig2File(configPath))
	return config, configPath
}

func TestLogoutRevokesRefreshTokenAndClears(t *testing.T) {
	config, configPath := newLoggedInConfig(t)
	revoker := &fakeRevoker{}

	require.NoError(t, Logout(context.Background(), config, configPath, revoker, io.Discard))

	// 优先撤销 refresh_token，成功即止（不再撤 access_token）
	require.Len(t, revoker.calls, 1)
	assert.Equal(t, revokeCall{token: "r-tok", hint: "refresh_token"}, revoker.calls[0])

	// 内存与磁盘 token 三字段均已清空
	assert.Empty(t, config.Feishu.UserAccessToken)
	assert.Empty(t, config.Feishu.RefreshToken)
	assert.Zero(t, config.Feishu.TokenExpireTime)

	onDisk, err := ReadConfigFromFile(configPath)
	require.NoError(t, err)
	assert.Empty(t, onDisk.Feishu.UserAccessToken)
	assert.Empty(t, onDisk.Feishu.RefreshToken)
	assert.Zero(t, onDisk.Feishu.TokenExpireTime)
	// 应用凭证不受影响
	assert.Equal(t, "app-id", onDisk.Feishu.AppId)
}

func TestLogoutRevokeFailureFallsBackAndStillClears(t *testing.T) {
	config, configPath := newLoggedInConfig(t)
	revoker := &fakeRevoker{err: errors.New("revoke boom")}
	var warn bytes.Buffer

	// revoke 失败不阻断本地清除
	require.NoError(t, Logout(context.Background(), config, configPath, revoker, &warn))

	// 先试 refresh_token，失败后再退回 access_token
	require.Len(t, revoker.calls, 2)
	assert.Equal(t, revokeCall{token: "r-tok", hint: "refresh_token"}, revoker.calls[0])
	assert.Equal(t, revokeCall{token: "u-tok", hint: "access_token"}, revoker.calls[1])
	assert.NotEmpty(t, warn.String(), "撤销失败应有 stderr 警告")

	onDisk, err := ReadConfigFromFile(configPath)
	require.NoError(t, err)
	assert.Empty(t, onDisk.Feishu.UserAccessToken)
	assert.Empty(t, onDisk.Feishu.RefreshToken)
}

func TestRevokeTokensBestEffortRefreshFirst(t *testing.T) {
	config := NewConfig("app-id", "app-secret")
	config.Feishu.UserAccessToken = "u-tok"
	config.Feishu.RefreshToken = "r-tok"
	revoker := &fakeRevoker{}

	RevokeTokensBestEffort(context.Background(), config, revoker, io.Discard)

	// 优先撤销 refresh_token，成功即止；不动 config 字段（清空是调用方的事）
	require.Len(t, revoker.calls, 1)
	assert.Equal(t, revokeCall{token: "r-tok", hint: "refresh_token"}, revoker.calls[0])
	assert.Equal(t, "u-tok", config.Feishu.UserAccessToken)
	assert.Equal(t, "r-tok", config.Feishu.RefreshToken)
}

func TestRevokeTokensBestEffortFailureFallsBack(t *testing.T) {
	config := NewConfig("app-id", "app-secret")
	config.Feishu.UserAccessToken = "u-tok"
	config.Feishu.RefreshToken = "r-tok"
	revoker := &fakeRevoker{err: errors.New("revoke boom")}
	var warn bytes.Buffer

	RevokeTokensBestEffort(context.Background(), config, revoker, &warn)

	require.Len(t, revoker.calls, 2)
	assert.Equal(t, revokeCall{token: "r-tok", hint: "refresh_token"}, revoker.calls[0])
	assert.Equal(t, revokeCall{token: "u-tok", hint: "access_token"}, revoker.calls[1])
	assert.NotEmpty(t, warn.String(), "撤销失败应有 warn 输出")
}

func TestRevokeTokensBestEffortNoTokensNoCalls(t *testing.T) {
	revoker := &fakeRevoker{}
	RevokeTokensBestEffort(context.Background(), NewConfig("app-id", "app-secret"), revoker, io.Discard)
	assert.Empty(t, revoker.calls)
}

func TestLogoutOnlyAccessTokenRevokesAccess(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := NewConfig("app-id", "app-secret")
	config.Feishu.UserAccessToken = "u-tok" // 无 refresh_token
	require.NoError(t, config.WriteConfig2File(configPath))

	revoker := &fakeRevoker{}
	require.NoError(t, Logout(context.Background(), config, configPath, revoker, io.Discard))

	require.Len(t, revoker.calls, 1)
	assert.Equal(t, revokeCall{token: "u-tok", hint: "access_token"}, revoker.calls[0])
	assert.Empty(t, config.Feishu.UserAccessToken)
}
