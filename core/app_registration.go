package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// 飞书应用注册端点：匿名 OAuth device flow（无需任何预置凭证），
// begin 与 poll 同 URL，靠 form 的 action 字段区分。
//
// 考证：lark CLI internal/auth/app_registration.go。
const appRegistrationPath = "/oauth/v1/app/registration"

// ErrLarkTenantNotSupported：poll 成功但 tenant_brand=lark 且 secret 为空
// （Lark 海外租户需切 accounts.larksuite.com 重 poll，larkdown 全栈仅支持 feishu 域，不移植）。
var ErrLarkTenantNotSupported = errors.New(
	"检测到 Lark（海外版）租户：larkdown 仅支持飞书（feishu.cn）域，请改用手动配置 larkdown config --appId --appSecret")

// AppRegistrar 是匿名 OAuth device flow 客户端：注册飞书 PersonalAgent 个人应用，
// 拿到新应用的 appId/appSecret。风格与 OAuthManager 对齐（纯裸 HTTP，不依赖 lark SDK）。
type AppRegistrar struct {
	accountsBaseURL string // begin + poll 都在 accounts 域；测试注入 httptest
	openBaseURL     string // 仅用于 verification_uri_complete 缺省时拼授权页链接
	httpClient      *http.Client
	debug           bool

	// 测试缝隙（生产为 nil / 0，走默认值）：
	sleep           func(context.Context, time.Duration) error // 轮询间隔等待；测试注入零等待并记录 interval
	maxPollAttempts int                                        // 轮询最大次数；测试设小值以快速触发 timeout 分支
}

// NewAppRegistrar 创建应用注册客户端。
func NewAppRegistrar(opts *ClientOptions) *AppRegistrar {
	return &AppRegistrar{
		accountsBaseURL: defaultAccountsBaseURL,
		openBaseURL:     defaultOpenBaseURL,
		httpClient:      &http.Client{Timeout: 15 * time.Second},
		debug:           opts != nil && opts.Debug,
	}
}

// AppRegistrationCode 是注册 begin 的返回（设备码授权入口）。
type AppRegistrationCode struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string // 手动兜底展示
	VerificationURIComplete string
	ExpiresIn               int // 设备码有效期（秒），缺省 300
	Interval                int // 轮询间隔（秒），缺省 5
}

// AppCredentials 是注册 poll 成功的返回（新创建应用的凭证）。
type AppCredentials struct {
	AppId       string // client_id
	AppSecret   string // client_secret
	OpenID      string // user_info.open_id
	TenantBrand string // user_info.tenant_brand（"lark" 在 core 层已转错误，此处恒为 "feishu" 或空）
}

// appRegistrationBeginResponse 是注册端点 action=begin 的响应。
type appRegistrationBeginResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

// appRegistrationPollResponse 是注册端点 action=poll 的响应。
type appRegistrationPollResponse struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	UserInfo     struct {
		OpenID      string `json:"open_id"`
		TenantBrand string `json:"tenant_brand"`
	} `json:"user_info"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// BeginAppRegistration 发起 PersonalAgent 应用注册（device flow 第一步）。
// 匿名请求，无 Authorization 头；archetype=PersonalAgent 即"个人应用"。
func (r *AppRegistrar) BeginAppRegistration(ctx context.Context) (*AppRegistrationCode, error) {
	form := url.Values{}
	form.Set("action", "begin")
	form.Set("archetype", "PersonalAgent")
	form.Set("auth_method", "client_secret")
	form.Set("request_user_info", "open_id tenant_brand")

	status, body, err := r.postForm(ctx, r.accountsBaseURL+appRegistrationPath, form)
	if err != nil {
		return nil, fmt.Errorf("发起应用注册失败: %w", err)
	}

	var data appRegistrationBeginResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("发起应用注册失败: HTTP %d 响应非 JSON: %s", status, strings.TrimSpace(string(body)))
	}
	if status >= 400 || data.Error != "" {
		return nil, fmt.Errorf("发起应用注册失败: %s", firstNonEmpty(data.ErrorDescription, data.Error, "未知错误"))
	}

	expiresIn := data.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 300
	}
	interval := data.Interval
	if interval == 0 {
		interval = 5
	}
	verificationURIComplete := data.VerificationURIComplete
	if verificationURIComplete == "" {
		// 授权页链接考证自 lark CLI：{open域}/page/cli?user_code=...
		verificationURIComplete = r.openBaseURL + "/page/cli?user_code=" + url.QueryEscape(data.UserCode)
	}

	return &AppRegistrationCode{
		DeviceCode:              data.DeviceCode,
		UserCode:                data.UserCode,
		VerificationURI:         data.VerificationURI,
		VerificationURIComplete: verificationURIComplete,
		ExpiresIn:               expiresIn,
		Interval:                interval,
	}, nil
}

// PollAppRegistration 轮询注册端点，直至用户完成授权、被拒或超时（device flow 第二步）。
// 每轮先等待 interval 秒，网络/解析错误按瞬时处理并退避重试。
func (r *AppRegistrar) PollAppRegistration(ctx context.Context, deviceCode string, interval, expiresIn int) (*AppCredentials, error) {
	if interval < 1 {
		interval = 5
	}
	if expiresIn <= 0 {
		expiresIn = 300
	}

	const maxPollInterval = 60
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	currentInterval := interval
	attempts := 0
	maxAttempts := r.pollAttemptsOrDefault()

	for time.Now().Before(deadline) && attempts < maxAttempts {
		attempts++
		if err := r.wait(ctx, time.Duration(currentInterval)*time.Second); err != nil {
			return nil, &DeviceFlowError{Reason: "expired_token", Message: "轮询已取消"}
		}

		form := url.Values{}
		form.Set("action", "poll")
		form.Set("device_code", deviceCode)

		_, body, err := r.postForm(ctx, r.accountsBaseURL+appRegistrationPath, form)
		if err != nil {
			currentInterval = bumpInterval(currentInterval, 1, maxPollInterval)
			continue
		}
		var data appRegistrationPollResponse
		if err := json.Unmarshal(body, &data); err != nil {
			currentInterval = bumpInterval(currentInterval, 1, maxPollInterval)
			continue
		}

		if data.Error == "" && data.ClientID != "" {
			if data.ClientSecret == "" {
				if data.UserInfo.TenantBrand == "lark" {
					return nil, ErrLarkTenantNotSupported
				}
				return nil, fmt.Errorf("应用注册响应缺少 client_secret，请重试或改用手动配置 larkdown config --appId --appSecret")
			}
			return &AppCredentials{
				AppId:       data.ClientID,
				AppSecret:   data.ClientSecret,
				OpenID:      data.UserInfo.OpenID,
				TenantBrand: data.UserInfo.TenantBrand,
			}, nil
		}

		switch data.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			currentInterval = bumpInterval(currentInterval, 5, maxPollInterval)
			continue
		case "access_denied":
			return nil, &DeviceFlowError{Reason: "access_denied", Message: firstNonEmpty(data.ErrorDescription, "用户拒绝了授权")}
		case "expired_token", "invalid_grant":
			return nil, &DeviceFlowError{Reason: "expired_token", Message: firstNonEmpty(data.ErrorDescription, "设备码已过期，请重试")}
		}

		return nil, &DeviceFlowError{Reason: "expired_token", Message: firstNonEmpty(data.ErrorDescription, data.Error, "未知错误")}
	}

	return nil, &DeviceFlowError{Reason: "expired_token", Message: "授权超时，请重试"}
}

// postForm 发一个匿名 form-encoded POST（注册端点不需要 Authorization 头）。
func (r *AppRegistrar) postForm(ctx context.Context, endpoint string, form url.Values) (int, []byte, error) {
	status, body, err := postForm(ctx, r.httpClient, endpoint, form, "")
	if r.debug {
		if err != nil {
			fmt.Fprintf(os.Stderr, "[larkdown][app-registration] POST %s -> transport error: %v\n", endpoint, err)
		} else {
			fmt.Fprintf(os.Stderr, "[larkdown][app-registration] POST %s -> HTTP %d\n", endpoint, status)
		}
	}
	return status, body, err
}

// wait 等待 d，生产走 time.After（可被 ctx 取消打断），测试注入 r.sleep 消除真实等待。
func (r *AppRegistrar) wait(ctx context.Context, d time.Duration) error {
	if r.sleep != nil {
		return r.sleep(ctx, d)
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pollAttemptsOrDefault 返回轮询次数上界（防御性兜底，正常由 deadline 先终止）。
func (r *AppRegistrar) pollAttemptsOrDefault() int {
	if r.maxPollAttempts > 0 {
		return r.maxPollAttempts
	}
	return 600
}
