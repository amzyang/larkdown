package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// TokenRefresher 抽象 token 刷新能力（consumer 侧接口），OAuthManager 天然满足。
type TokenRefresher interface {
	RefreshUserToken(ctx context.Context, refreshToken string) (*TokenResult, error)
}

// RefreshOutcome 表示一次 token 刷新的结果分级。
type RefreshOutcome int

const (
	// RefreshOutcomeRefreshed 本进程成功刷新并落盘。
	RefreshOutcomeRefreshed RefreshOutcome = iota
	// RefreshOutcomeRefreshedByOther 锁内重读发现另一进程已刷新，直接复用磁盘上的新 token。
	RefreshOutcomeRefreshedByOther
	// RefreshOutcomeTokenInvalidCleared refresh_token 确定性失效，token 三字段已清空并落盘，需重新 login。
	RefreshOutcomeTokenInvalidCleared
	// RefreshOutcomeTransientFailure 瞬时失败（网络/限流/服务端抖动），config 保持不变，下次自动重试。
	RefreshOutcomeTransientFailure
)

const (
	refreshLockTimeout       = 30 * time.Second
	refreshLockRetryInterval = 500 * time.Millisecond
)

// EnsureFreshUserToken 在跨进程 flock 保护下刷新 user_access_token 并写回 configPath。
// 飞书 refresh_token 是轮换式（刷新后旧值失效），并发刷新会互相打翻导致 token 永久失效，
// 因此先对锁文件（与 configPath 同目录，锁与被保护资源同域）加锁，再在锁内重读磁盘做 double-check。
// 调用方负责先确认 NeedsRefresh()。
func EnsureFreshUserToken(ctx context.Context, config *Config, configPath string, refresher TokenRefresher) (RefreshOutcome, error) {
	fileLock := flock.New(filepath.Join(filepath.Dir(configPath), "refresh.lock"))
	lockCtx, cancel := context.WithTimeout(ctx, refreshLockTimeout)
	defer cancel()
	locked, err := fileLock.TryLockContext(lockCtx, refreshLockRetryInterval)
	if err != nil {
		return RefreshOutcomeTransientFailure, fmt.Errorf("获取刷新锁失败: %w", err)
	}
	if !locked {
		return RefreshOutcomeTransientFailure, errors.New("等待刷新锁超时")
	}
	defer fileLock.Unlock()

	// double-check：锁内重读磁盘，另一进程可能已完成刷新（或已清除失效 token）
	if fresh, err := ReadConfigFromFile(configPath); err == nil {
		copyTokenFields(config, &fresh.Feishu)
		if config.Feishu.HasValidUserToken() {
			return RefreshOutcomeRefreshedByOther, nil
		}
	}
	if config.Feishu.RefreshToken == "" {
		return RefreshOutcomeTokenInvalidCleared, errors.New("refresh_token 已被其他进程清除")
	}
	// refresh_token 已确定过期：跳过注定失败的网络刷新，直接清 token 提示重登
	if config.Feishu.RefreshTokenExpired() {
		config.Feishu.UserAccessToken = ""
		config.Feishu.RefreshToken = ""
		config.Feishu.TokenExpireTime = 0
		config.Feishu.RefreshTokenExpireTime = 0
		if werr := config.WriteConfig2File(configPath); werr != nil {
			return RefreshOutcomeTokenInvalidCleared, fmt.Errorf("清除失效 token 写入配置失败: %w", werr)
		}
		return RefreshOutcomeTokenInvalidCleared, errors.New("refresh_token 已过期，请重新登录")
	}

	result, err := refresher.RefreshUserToken(ctx, config.Feishu.RefreshToken)
	if err != nil && isTransientRefreshError(err) {
		// 瞬时失败原地重试一次
		result, err = refresher.RefreshUserToken(ctx, config.Feishu.RefreshToken)
	}
	switch {
	case err == nil:
		now := time.Now().Unix()
		config.Feishu.UserAccessToken = result.UserAccessToken
		config.Feishu.RefreshToken = result.RefreshToken
		config.Feishu.TokenExpireTime = now + result.ExpiresIn
		if result.RefreshExpiresIn > 0 {
			config.Feishu.RefreshTokenExpireTime = now + result.RefreshExpiresIn
		}
		if werr := config.WriteConfig2File(configPath); werr != nil {
			return RefreshOutcomeRefreshed, fmt.Errorf("token 已刷新但写入配置失败: %w", werr)
		}
		return RefreshOutcomeRefreshed, nil
	case isTransientRefreshError(err):
		return RefreshOutcomeTransientFailure, err
	default:
		// 确定性失效：清除坏 token 并落盘，避免后续每个命令重复失败
		config.Feishu.UserAccessToken = ""
		config.Feishu.RefreshToken = ""
		config.Feishu.TokenExpireTime = 0
		config.Feishu.RefreshTokenExpireTime = 0
		if werr := config.WriteConfig2File(configPath); werr != nil {
			return RefreshOutcomeTokenInvalidCleared, fmt.Errorf("清除失效 token 写入配置失败: %w", werr)
		}
		return RefreshOutcomeTokenInvalidCleared, err
	}
}

// copyTokenFields 把磁盘上最新的 token 字段同步进内存 config。
func copyTokenFields(dst *Config, src *FeishuConfig) {
	dst.Feishu.UserAccessToken = src.UserAccessToken
	dst.Feishu.RefreshToken = src.RefreshToken
	dst.Feishu.TokenExpireTime = src.TokenExpireTime
	dst.Feishu.RefreshTokenExpireTime = src.RefreshTokenExpireTime
}

// isTransientRefreshError 判定刷新失败是否为瞬时错误（可重试、不清 token）。
// v2 刷新端点（/open-apis/authen/v2/oauth/token）返回 OAuth2 风格错误或飞书业务码，
// 由 *RefreshTokenError 承载（见 oauth.go）。分级哲学与旧 v1 一致：
//   - 服务端给了结构化业务/OAuth 错误 → 默认确定性失效（清 token、提示重登）
//   - 够不到服务端 / 5xx / 429 / 明确的临时错误 → 瞬时（保留 token、重试）
//
// 瞬时：传输层错误、context 超时、body 读取/解析失败、缺 access_token（非 *RefreshTokenError）；
//
//	HTTP 5xx/429；OAuth server_error/temporarily_unavailable/slow_down；飞书码 20050/99991400。
//
// 确定性失效：invalid_grant/invalid_request/invalid_client 等其余结构化 4xx。
func isTransientRefreshError(err error) bool {
	var rerr *RefreshTokenError
	if !errors.As(err, &rerr) {
		return true
	}
	if rerr.HTTPStatus >= 500 || rerr.HTTPStatus == http.StatusTooManyRequests {
		return true
	}
	switch rerr.OAuthError {
	case "server_error", "temporarily_unavailable", "slow_down":
		return true
	}
	switch rerr.FeishuCode {
	case 20050, 99991400:
		return true
	}
	return false
}
