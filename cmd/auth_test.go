package main

import (
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkdown/core"
	"github.com/stretchr/testify/assert"
)

func TestFormatAuthStatusValidUserToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := core.FeishuConfig{
		AppId:           "cli_app_id",
		UserAccessToken: "u-tok",
		RefreshToken:    "r-tok",
		TokenExpireTime: now.Unix() + 3600, // 距过期 1h，> 5min 提前量 → 有效
	}

	joined := strings.Join(formatAuthStatus(cfg, "/path/config.json", now), "\n")

	assert.Contains(t, joined, "配置文件: /path/config.json")
	assert.Contains(t, joined, "app_id: cli_app_id")
	assert.Contains(t, joined, "user_access_token（有效")
}

func TestFormatAuthStatusExpiredNeedsRefresh(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := core.FeishuConfig{
		AppId:           "cli_app_id",
		UserAccessToken: "u-tok",
		RefreshToken:    "r-tok",
		TokenExpireTime: now.Unix() - 10, // 已过期，但有 refresh_token
	}

	joined := strings.Join(formatAuthStatus(cfg, "/p", now), "\n")

	assert.Contains(t, joined, "user_access_token（已过期")
	assert.Contains(t, joined, "自动刷新")
}

func TestFormatAuthStatusNoUserToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := core.FeishuConfig{AppId: "cli_app_id"}

	joined := strings.Join(formatAuthStatus(cfg, "/p", now), "\n")

	assert.Contains(t, joined, "tenant_access_token（应用凭证）")
	assert.Contains(t, joined, "larkdown auth login")
}

func TestFormatAuthStatusWithinLeewayCountsAsExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := core.FeishuConfig{
		AppId:           "cli_app_id",
		UserAccessToken: "u-tok",
		RefreshToken:    "r-tok",
		TokenExpireTime: now.Unix() + 100, // 剩余 100s < 300s 提前量 → 视为过期
	}

	joined := strings.Join(formatAuthStatus(cfg, "/p", now), "\n")

	assert.Contains(t, joined, "已过期")
}
