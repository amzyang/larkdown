package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRefresher 可编排的 TokenRefresher fake：按调用次序依序返回预设结果。
type fakeRefresher struct {
	mu      sync.Mutex
	calls   int
	results []fakeRefreshResult
}

type fakeRefreshResult struct {
	token *TokenResult
	err   error
}

func (f *fakeRefresher) RefreshUserToken(ctx context.Context, refreshToken string) (*TokenResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.calls
	if idx >= len(f.results) {
		idx = len(f.results) - 1
	}
	f.calls++
	r := f.results[idx]
	return r.token, r.err
}

func (f *fakeRefresher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newExpiredConfig 在临时目录写入一份「token 已过期、待刷新」的 config，返回内存副本与路径。
func newExpiredConfig(t *testing.T) (*Config, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := NewConfig("app-id", "app-secret")
	config.Feishu.UserAccessToken = "u-old"
	config.Feishu.RefreshToken = "ur-old"
	config.Feishu.TokenExpireTime = time.Now().Unix() - 100
	require.NoError(t, config.WriteConfig2File(configPath))
	return config, configPath
}

func TestEnsureFreshUserToken_Success(t *testing.T) {
	config, configPath := newExpiredConfig(t)
	refresher := &fakeRefresher{results: []fakeRefreshResult{
		{token: &TokenResult{UserAccessToken: "u-new", RefreshToken: "ur-new", ExpiresIn: 7200, RefreshExpiresIn: 604800}},
	}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.NoError(t, err)
	assert.Equal(t, RefreshOutcomeRefreshed, outcome)
	assert.Equal(t, 1, refresher.callCount())
	assert.Equal(t, "u-new", config.Feishu.UserAccessToken)
	assert.Equal(t, "ur-new", config.Feishu.RefreshToken)
	assert.Greater(t, config.Feishu.TokenExpireTime, time.Now().Unix())
	assert.Greater(t, config.Feishu.RefreshTokenExpireTime, time.Now().Unix())

	// 已落盘
	onDisk, err := ReadConfigFromFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, config.Feishu, onDisk.Feishu)
}

func TestEnsureFreshUserToken_RefreshedByOther(t *testing.T) {
	config, configPath := newExpiredConfig(t)

	// 模拟另一进程已完成刷新并落盘：磁盘上的 token 有效，内存副本仍是过期旧值
	other := NewConfig("app-id", "app-secret")
	other.Feishu.UserAccessToken = "u-other"
	other.Feishu.RefreshToken = "ur-other"
	other.Feishu.TokenExpireTime = time.Now().Unix() + 7200
	require.NoError(t, other.WriteConfig2File(configPath))

	refresher := &fakeRefresher{results: []fakeRefreshResult{
		{err: errors.New("should not be called")},
	}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.NoError(t, err)
	assert.Equal(t, RefreshOutcomeRefreshedByOther, outcome)
	assert.Equal(t, 0, refresher.callCount())
	assert.Equal(t, other.Feishu, config.Feishu)
}

func TestEnsureFreshUserToken_DeterministicFailureClearsToken(t *testing.T) {
	config, configPath := newExpiredConfig(t)
	// 模拟 OAuthManager 的包装形态：fmt.Errorf("%w", *RefreshTokenError)
	refreshErr := fmt.Errorf("刷新 token 失败: %w", &RefreshTokenError{OAuthError: "invalid_grant", HTTPStatus: http.StatusBadRequest})
	refresher := &fakeRefresher{results: []fakeRefreshResult{{err: refreshErr}}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.Error(t, err)
	assert.Equal(t, RefreshOutcomeTokenInvalidCleared, outcome)
	assert.Equal(t, 1, refresher.callCount(), "确定性失效不应重试")
	assert.Empty(t, config.Feishu.UserAccessToken)
	assert.Empty(t, config.Feishu.RefreshToken)
	assert.Zero(t, config.Feishu.TokenExpireTime)

	// 清除已落盘
	onDisk, readErr := ReadConfigFromFile(configPath)
	require.NoError(t, readErr)
	assert.Empty(t, onDisk.Feishu.UserAccessToken)
	assert.Empty(t, onDisk.Feishu.RefreshToken)
	assert.Zero(t, onDisk.Feishu.TokenExpireTime)
}

func TestEnsureFreshUserToken_TransientFailureRetriesOnce(t *testing.T) {
	config, configPath := newExpiredConfig(t)
	refresher := &fakeRefresher{results: []fakeRefreshResult{
		{err: errors.New("dial tcp: i/o timeout")},
	}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.Error(t, err)
	assert.Equal(t, RefreshOutcomeTransientFailure, outcome)
	assert.Equal(t, 2, refresher.callCount(), "瞬时失败应原地重试一次")
	// config 保持不变（内存与磁盘）
	assert.Equal(t, "u-old", config.Feishu.UserAccessToken)
	assert.Equal(t, "ur-old", config.Feishu.RefreshToken)
	onDisk, readErr := ReadConfigFromFile(configPath)
	require.NoError(t, readErr)
	assert.Equal(t, "u-old", onDisk.Feishu.UserAccessToken)
	assert.Equal(t, "ur-old", onDisk.Feishu.RefreshToken)
}

func TestEnsureFreshUserToken_TransientThenSuccess(t *testing.T) {
	config, configPath := newExpiredConfig(t)
	refresher := &fakeRefresher{results: []fakeRefreshResult{
		{err: errors.New("dial tcp: i/o timeout")},
		{token: &TokenResult{UserAccessToken: "u-new", RefreshToken: "ur-new", ExpiresIn: 7200}},
	}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.NoError(t, err)
	assert.Equal(t, RefreshOutcomeRefreshed, outcome)
	assert.Equal(t, 2, refresher.callCount())
	assert.Equal(t, "u-new", config.Feishu.UserAccessToken)
}

func TestEnsureFreshUserToken_Concurrent(t *testing.T) {
	config, configPath := newExpiredConfig(t)
	refresher := &fakeRefresher{results: []fakeRefreshResult{
		{token: &TokenResult{UserAccessToken: "u-new", RefreshToken: "ur-new", ExpiresIn: 7200}},
	}}

	// 两个并发调用各持独立的内存 config 副本（gofrs/flock 每个实例独立 fd，进程内也互斥）
	configA := *config
	configB := *config
	outcomes := make([]RefreshOutcome, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		outcome, err := EnsureFreshUserToken(context.Background(), &configA, configPath, refresher)
		require.NoError(t, err)
		outcomes[0] = outcome
	}()
	go func() {
		defer wg.Done()
		outcome, err := EnsureFreshUserToken(context.Background(), &configB, configPath, refresher)
		require.NoError(t, err)
		outcomes[1] = outcome
	}()
	wg.Wait()

	assert.Equal(t, 1, refresher.callCount(), "并发下只应有一次真实刷新")
	assert.ElementsMatch(t, []RefreshOutcome{RefreshOutcomeRefreshed, RefreshOutcomeRefreshedByOther}, outcomes)
	assert.Equal(t, "u-new", configA.Feishu.UserAccessToken)
	assert.Equal(t, "u-new", configB.Feishu.UserAccessToken)
}

func TestIsTransientRefreshError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		transient bool
	}{
		{"invalid_grant 确定性", &RefreshTokenError{OAuthError: "invalid_grant", HTTPStatus: 400}, false},
		{"invalid_request 确定性", &RefreshTokenError{OAuthError: "invalid_request", HTTPStatus: 400}, false},
		{"invalid_client 确定性", &RefreshTokenError{OAuthError: "invalid_client", HTTPStatus: 400}, false},
		{"纯 4xx 无 error 字段确定性", &RefreshTokenError{HTTPStatus: 400}, false},
		{"HTTP 500 瞬时", &RefreshTokenError{HTTPStatus: 500}, true},
		{"HTTP 503 瞬时", &RefreshTokenError{HTTPStatus: 503}, true},
		{"HTTP 429 瞬时", &RefreshTokenError{HTTPStatus: 429}, true},
		{"server_error 瞬时", &RefreshTokenError{OAuthError: "server_error", HTTPStatus: 400}, true},
		{"temporarily_unavailable 瞬时", &RefreshTokenError{OAuthError: "temporarily_unavailable", HTTPStatus: 400}, true},
		{"飞书码 20050 瞬时", &RefreshTokenError{FeishuCode: 20050}, true},
		{"飞书码 99991400 瞬时", &RefreshTokenError{FeishuCode: 99991400}, true},
		{"飞书码 20037 确定性", &RefreshTokenError{FeishuCode: 20037}, false},
		{"包装后确定性", fmt.Errorf("刷新 token 失败: %w", &RefreshTokenError{OAuthError: "invalid_grant", HTTPStatus: 400}), false},
		{"包装后瞬时", fmt.Errorf("刷新 token 失败: %w", &RefreshTokenError{HTTPStatus: 500}), true},
		{"传输层错误", errors.New("dial tcp: i/o timeout"), true},
		{"context 超时", context.DeadlineExceeded, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.transient, isTransientRefreshError(tc.err))
		})
	}
}

func TestEnsureFreshUserToken_RefreshTokenExpiredShortCircuits(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	config := NewConfig("app-id", "app-secret")
	config.Feishu.UserAccessToken = "u-old"
	config.Feishu.RefreshToken = "ur-old"
	config.Feishu.TokenExpireTime = time.Now().Unix() - 100
	config.Feishu.RefreshTokenExpireTime = time.Now().Unix() - 50 // refresh_token 也已过期
	require.NoError(t, config.WriteConfig2File(configPath))

	refresher := &fakeRefresher{results: []fakeRefreshResult{{err: errors.New("should not be called")}}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.Error(t, err)
	assert.Equal(t, RefreshOutcomeTokenInvalidCleared, outcome)
	assert.Equal(t, 0, refresher.callCount(), "refresh_token 已过期应短路，不触发网络刷新")
	assert.Empty(t, config.Feishu.UserAccessToken)
	assert.Empty(t, config.Feishu.RefreshToken)
	assert.Zero(t, config.Feishu.RefreshTokenExpireTime)

	onDisk, readErr := ReadConfigFromFile(configPath)
	require.NoError(t, readErr)
	assert.Empty(t, onDisk.Feishu.RefreshToken)
}

func TestEnsureFreshUserToken_LegacyNoRefreshExpiryStillAttempts(t *testing.T) {
	config, configPath := newExpiredConfig(t) // RefreshTokenExpireTime == 0（v1 老 config）
	refresher := &fakeRefresher{results: []fakeRefreshResult{
		{token: &TokenResult{UserAccessToken: "u-new", RefreshToken: "ur-new", ExpiresIn: 7200, RefreshExpiresIn: 604800}},
	}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.NoError(t, err)
	assert.Equal(t, RefreshOutcomeRefreshed, outcome)
	assert.GreaterOrEqual(t, refresher.callCount(), 1, "无过期时间不应短路")
	assert.Greater(t, config.Feishu.RefreshTokenExpireTime, time.Now().Unix())
}
