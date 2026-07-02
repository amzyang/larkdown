package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chyroc/lark"
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
		{token: &TokenResult{UserAccessToken: "u-new", RefreshToken: "ur-new", ExpiresIn: 7200}},
	}}

	outcome, err := EnsureFreshUserToken(context.Background(), config, configPath, refresher)

	require.NoError(t, err)
	assert.Equal(t, RefreshOutcomeRefreshed, outcome)
	assert.Equal(t, 1, refresher.callCount())
	assert.Equal(t, "u-new", config.Feishu.UserAccessToken)
	assert.Equal(t, "ur-new", config.Feishu.RefreshToken)
	assert.Greater(t, config.Feishu.TokenExpireTime, time.Now().Unix())

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
	// 模拟 OAuthManager 的包装形态：fmt.Errorf("%w", *lark.Error)
	refreshErr := fmt.Errorf("刷新 token 失败: %w", &lark.Error{Code: 20037, Msg: "refresh token expired"})
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
		{"refresh_token 旧格式无效 20026", &lark.Error{Code: 20026}, false},
		{"refresh_token 过期 20037", &lark.Error{Code: 20037}, false},
		{"refresh_token 被撤销 20064", &lark.Error{Code: 20064}, false},
		{"refresh_token 已被使用 20073", &lark.Error{Code: 20073}, false},
		{"刷新端点服务端错误 20050", &lark.Error{Code: 20050}, true},
		{"限流 99991400", &lark.Error{Code: 99991400}, true},
		{"包装后的确定性错误", fmt.Errorf("刷新 token 失败: %w", &lark.Error{Code: 20064}), false},
		{"包装后的瞬时错误", fmt.Errorf("刷新 token 失败: %w", &lark.Error{Code: 20050}), true},
		{"传输层错误", errors.New("dial tcp: i/o timeout"), true},
		{"context 超时", context.DeadlineExceeded, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.transient, isTransientRefreshError(tc.err))
		})
	}
}
