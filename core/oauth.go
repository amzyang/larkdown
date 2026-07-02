package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// 飞书 OAuth 2.0 端点。device flow 分布在两个域：
//   - accounts 域：设备码申请（device_authorization）与 token 撤销（revoke）
//   - open 域：v2 统一 token 端点（设备码轮询换 token、refresh_token 刷新）
//
// 考证：lark CLI internal/auth（ResolveOAuthEndpoints / paths.go）。
const (
	defaultAccountsBaseURL  = "https://accounts.feishu.cn"
	defaultOpenBaseURL      = "https://open.feishu.cn"
	deviceAuthorizationPath = "/oauth/v1/device_authorization"
	revokePath              = "/oauth/v1/revoke"
	tokenV2Path             = "/open-apis/authen/v2/oauth/token"
)

// DeviceFlowScope 是设备码申请请求的 scope（登录所需的全部读写权限）。
// RequestDeviceCode 会在其上自动追加 offline_access 以换取 refresh_token。
const DeviceFlowScope = "docx:document:readonly docs:document.content:read wiki:wiki:readonly drive:file:readonly drive:drive:readonly bitable:app:readonly drive:export:readonly docs:document.media:download board:whiteboard:node:read docs:document.comment:read contact:contact.base:readonly wiki:node:create docx:document.block:convert docx:document:write_only docs:document.media:upload board:whiteboard:node:create spark:app:write"

// OAuthManager 是一个纯裸-HTTP 的飞书 OAuth 客户端（设备码流程）。
// 不依赖 lark SDK：设备码申请/轮询/刷新/撤销全部走 net/http + form-encoded。
type OAuthManager struct {
	appId           string
	appSecret       string
	accountsBaseURL string // accounts 域：device_authorization + revoke。默认飞书 accounts 域；测试注入
	openBaseURL     string // open 域：v2 token（poll + refresh）。默认飞书 open 域；测试注入
	httpClient      *http.Client
	debug           bool // 取自 ClientOptions.Debug，开启时对每次 OAuth 请求打一行 stderr 日志（不含 secret）

	// 测试缝隙（生产为 nil / 0，走默认值）：
	sleep           func(context.Context, time.Duration) error // 轮询间隔等待；测试注入零等待并记录 interval
	maxPollAttempts int                                        // 轮询最大次数；测试设小值以快速触发 timeout 分支
}

// NewOAuthManager 创建 OAuth 管理器。
func NewOAuthManager(appId, appSecret string, opts *ClientOptions) *OAuthManager {
	return &OAuthManager{
		appId:           appId,
		appSecret:       appSecret,
		accountsBaseURL: defaultAccountsBaseURL,
		openBaseURL:     defaultOpenBaseURL,
		httpClient:      &http.Client{Timeout: 15 * time.Second},
		debug:           opts != nil && opts.Debug,
	}
}

// TokenResult OAuth token 结果。
type TokenResult struct {
	UserAccessToken  string
	RefreshToken     string
	ExpiresIn        int64 // access_token 有效期（秒）
	RefreshExpiresIn int64 // refresh_token 有效期（秒）
}

// DeviceCodeResult 是设备码申请（device_authorization）的返回。
type DeviceCodeResult struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int // 设备码有效期（秒），缺省 240
	Interval                int // 轮询间隔（秒），缺省 5
}

// DeviceFlowError 表示设备码流程被服务端终止（用户拒绝 / 设备码过期）。
type DeviceFlowError struct {
	Reason  string // access_denied / expired_token
	Message string
}

func (e *DeviceFlowError) Error() string {
	return fmt.Sprintf("设备授权失败[%s]: %s", e.Reason, e.Message)
}

// RefreshTokenError 是 v2 刷新端点返回的结构化错误（OAuth2 风格 + 飞书扩展）。
// token_refresh.go 的 isTransientRefreshError 依据其字段判定瞬时 / 确定性失效。
type RefreshTokenError struct {
	OAuthError  string // OAuth2 error：invalid_grant / invalid_request / invalid_client / server_error …
	Description string // error_description 或 msg
	FeishuCode  int    // HTTP 200 但 code!=0 时的飞书业务码（无则 0）
	HTTPStatus  int
}

func (e *RefreshTokenError) Error() string {
	return fmt.Sprintf("刷新 token 失败: HTTP %d oauth_error=%q code=%d: %s", e.HTTPStatus, e.OAuthError, e.FeishuCode, e.Description)
}

// deviceAuthResponse 是 device_authorization 端点的响应。
type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

// tokenEndpointResponse 覆盖 v2 token 端点的成功、OAuth2 错误与飞书业务错误三种形态。
type tokenEndpointResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	Scope                 string `json:"scope"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
	Code                  int    `json:"code"`
	Msg                   string `json:"msg"`
}

// RequestDeviceCode 向 accounts 域申请设备码（device flow 第一步）。
// 采用 Basic auth（base64(appId:appSecret)）；scope 不含 offline_access 时自动追加。
func (m *OAuthManager) RequestDeviceCode(ctx context.Context, scope string) (*DeviceCodeResult, error) {
	if !strings.Contains(scope, "offline_access") {
		if scope != "" {
			scope += " offline_access"
		} else {
			scope = "offline_access"
		}
	}

	basicAuth := base64.StdEncoding.EncodeToString([]byte(m.appId + ":" + m.appSecret))
	form := url.Values{}
	form.Set("client_id", m.appId)
	form.Set("scope", scope)

	status, body, err := m.postForm(ctx, m.accountsBaseURL+deviceAuthorizationPath, form, basicAuth)
	if err != nil {
		return nil, fmt.Errorf("申请设备码失败: %w", err)
	}

	var data deviceAuthResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("申请设备码失败: HTTP %d 响应非 JSON: %s", status, strings.TrimSpace(string(body)))
	}
	if status >= 400 || data.Error != "" {
		return nil, fmt.Errorf("申请设备码失败: %s", firstNonEmpty(data.ErrorDescription, data.Error, "未知错误"))
	}

	expiresIn := data.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 240
	}
	interval := data.Interval
	if interval == 0 {
		interval = 5
	}
	verificationURIComplete := data.VerificationURIComplete
	if verificationURIComplete == "" {
		verificationURIComplete = data.VerificationURI
	}

	return &DeviceCodeResult{
		DeviceCode:              data.DeviceCode,
		UserCode:                data.UserCode,
		VerificationURI:         data.VerificationURI,
		VerificationURIComplete: verificationURIComplete,
		ExpiresIn:               expiresIn,
		Interval:                interval,
	}, nil
}

// PollDeviceToken 轮询 v2 token 端点，直至用户完成授权、被拒或超时（device flow 第二步）。
// 每轮先等待 interval 秒（OAuth 设备码规范要求首次轮询前也等待），网络/解析错误按瞬时处理并退避重试。
func (m *OAuthManager) PollDeviceToken(ctx context.Context, deviceCode string, interval, expiresIn int) (*TokenResult, error) {
	if interval < 1 {
		interval = 5
	}
	if expiresIn <= 0 {
		expiresIn = 240
	}

	const maxPollInterval = 60
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	currentInterval := interval
	attempts := 0
	maxAttempts := m.pollAttemptsOrDefault()

	for time.Now().Before(deadline) && attempts < maxAttempts {
		attempts++
		if err := m.wait(ctx, time.Duration(currentInterval)*time.Second); err != nil {
			return nil, &DeviceFlowError{Reason: "expired_token", Message: "轮询已取消"}
		}

		form := url.Values{}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		form.Set("device_code", deviceCode)
		form.Set("client_id", m.appId)
		form.Set("client_secret", m.appSecret)

		_, body, err := m.postForm(ctx, m.openBaseURL+tokenV2Path, form, "")
		if err != nil {
			currentInterval = bumpInterval(currentInterval, 1, maxPollInterval)
			continue
		}
		var data tokenEndpointResponse
		if err := json.Unmarshal(body, &data); err != nil {
			currentInterval = bumpInterval(currentInterval, 1, maxPollInterval)
			continue
		}

		if data.Error == "" && data.AccessToken != "" {
			return newTokenResult(&data), nil
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

// RefreshUserToken 用 refresh_token 向 v2 token 端点换新 token（grant_type=refresh_token）。
// 与设备码轮询共用同一 token 端点，但用 form 里的 client_secret（非 Basic auth）。
func (m *OAuthManager) RefreshUserToken(ctx context.Context, refreshToken string) (*TokenResult, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", m.appId)
	form.Set("client_secret", m.appSecret)

	status, body, err := m.postForm(ctx, m.openBaseURL+tokenV2Path, form, "")
	if err != nil {
		// 传输层错误：非 *RefreshTokenError → isTransientRefreshError 判定瞬时
		return nil, fmt.Errorf("刷新 token 失败: %w", err)
	}

	var data tokenEndpointResponse
	if err := json.Unmarshal(body, &data); err != nil {
		if status >= 400 {
			return nil, &RefreshTokenError{HTTPStatus: status, Description: strings.TrimSpace(string(body))}
		}
		return nil, fmt.Errorf("刷新 token 失败: 响应非 JSON: %w", err)
	}
	if status >= 400 || data.Error != "" || data.Code != 0 {
		return nil, &RefreshTokenError{
			OAuthError:  data.Error,
			Description: firstNonEmpty(data.ErrorDescription, data.Msg),
			FeishuCode:  data.Code,
			HTTPStatus:  status,
		}
	}
	if data.AccessToken == "" {
		return nil, fmt.Errorf("刷新 token 失败: 响应无 access_token")
	}

	result := newTokenResult(&data)
	if result.RefreshToken == "" {
		result.RefreshToken = refreshToken // 未轮换，回落入参
	}
	return result, nil
}

// RevokeToken 撤销一枚已签发的 OAuth token（best-effort 登出用）。
// POST <accounts 域>/oauth/v1/revoke，application/x-www-form-urlencoded。
// tokenTypeHint 取 "refresh_token" 或 "access_token"。
func (m *OAuthManager) RevokeToken(ctx context.Context, token, tokenTypeHint string) error {
	form := url.Values{}
	form.Set("client_id", m.appId)
	form.Set("client_secret", m.appSecret)
	form.Set("token", token)
	if tokenTypeHint != "" {
		form.Set("token_type_hint", tokenTypeHint)
	}

	status, body, err := m.postForm(ctx, m.accountsBaseURL+revokePath, form, "")
	if err != nil {
		return fmt.Errorf("revoke 请求失败: %w", err)
	}
	if status >= 400 {
		return fmt.Errorf("revoke 失败: HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	// 飞书业务错误：HTTP 200 但 body 里 code != 0
	if len(body) > 0 {
		var data struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(body, &data); err == nil && data.Code != 0 {
			return fmt.Errorf("revoke 失败 [%d]: %s", data.Code, data.Msg)
		}
	}
	return nil
}

// postForm 发一个 form-encoded POST 并返回状态码与响应体。
// basicAuth 非空时设置 Authorization: Basic 头（仅设备码申请用）。
func (m *OAuthManager) postForm(ctx context.Context, endpoint string, form url.Values, basicAuth string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basicAuth != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		m.debugf("POST %s -> transport error: %v", endpoint, err)
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	m.debugf("POST %s -> HTTP %d", endpoint, resp.StatusCode)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// wait 等待 d，生产走 time.After（可被 ctx 取消打断），测试注入 m.sleep 消除真实等待。
func (m *OAuthManager) wait(ctx context.Context, d time.Duration) error {
	if m.sleep != nil {
		return m.sleep(ctx, d)
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pollAttemptsOrDefault 返回轮询次数上界（防御性兜底，正常由 deadline 先终止）。
func (m *OAuthManager) pollAttemptsOrDefault() int {
	if m.maxPollAttempts > 0 {
		return m.maxPollAttempts
	}
	return 600
}

// debugf 仅在 --debug 下对 OAuth 请求打一行 stderr 日志（只记 endpoint + 状态，不含 secret/token）。
func (m *OAuthManager) debugf(format string, args ...interface{}) {
	if m.debug {
		fmt.Fprintf(os.Stderr, "[larkdown][oauth] "+format+"\n", args...)
	}
}

// newTokenResult 把 v2 token 端点的成功响应转成 TokenResult，套用缺省有效期。
func newTokenResult(d *tokenEndpointResponse) *TokenResult {
	expiresIn := d.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 7200
	}
	refreshExpiresIn := d.RefreshTokenExpiresIn
	if refreshExpiresIn == 0 {
		refreshExpiresIn = 604800
	}
	return &TokenResult{
		UserAccessToken:  d.AccessToken,
		RefreshToken:     d.RefreshToken,
		ExpiresIn:        expiresIn,
		RefreshExpiresIn: refreshExpiresIn,
	}
}

// bumpInterval 把轮询间隔增加 delta，封顶 max。
func bumpInterval(cur, delta, max int) int {
	cur += delta
	if cur > max {
		cur = max
	}
	return cur
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
