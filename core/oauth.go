package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chyroc/lark"
)

const (
	DefaultOAuthPort    = 9999
	DefaultRedirectPath = "/callback"
)

// 飞书 OAuth token 撤销端点（accounts 域，非 open 域）。
// 考证：lark CLI ResolveOAuthEndpoints = ep.Accounts("https://accounts.feishu.cn") + "/oauth/v1/revoke"。
const (
	defaultRevokeBaseURL = "https://accounts.feishu.cn"
	revokePath           = "/oauth/v1/revoke"
)

// OAuthManager 管理 OAuth 流程
type OAuthManager struct {
	appId         string
	appSecret     string
	redirectURI   string
	larkClient    *lark.Lark
	revokeBaseURL string // token 撤销端点 base，默认飞书 accounts 域；测试用 httptest.Server 注入
	httpClient    *http.Client
}

// NewOAuthManager 创建 OAuth 管理器
func NewOAuthManager(appId, appSecret string, port int, opts *ClientOptions) *OAuthManager {
	redirectURI := fmt.Sprintf("http://localhost:%d%s", port, DefaultRedirectPath)
	larkOpts := []lark.ClientOptionFunc{
		lark.WithAppCredential(appId, appSecret),
	}
	if opts != nil && opts.Debug {
		larkOpts = append(larkOpts, lark.WithLogger(lark.NewLoggerStdout(), lark.LogLevelTrace))
	}
	return &OAuthManager{
		appId:         appId,
		appSecret:     appSecret,
		redirectURI:   redirectURI,
		larkClient:    lark.New(larkOpts...),
		revokeBaseURL: defaultRevokeBaseURL,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

// GetAuthURL 生成授权 URL
func (m *OAuthManager) GetAuthURL(state string) string {
	// 请求必要的权限 scope
	scope := "docx:document:readonly docs:document.content:read wiki:wiki:readonly drive:file:readonly drive:drive:readonly bitable:app:readonly drive:export:readonly docs:document.media:download board:whiteboard:node:read docs:document.comment:read contact:contact.base:readonly wiki:node:create docx:document.block:convert docx:document:write_only docs:document.media:upload board:whiteboard:node:create spark:app:write"
	return fmt.Sprintf(
		"https://open.feishu.cn/open-apis/authen/v1/authorize?app_id=%s&redirect_uri=%s&state=%s&scope=%s",
		m.appId,
		url.QueryEscape(m.redirectURI),
		url.QueryEscape(state),
		url.QueryEscape(scope),
	)
}

// TokenResult OAuth token 结果
type TokenResult struct {
	UserAccessToken string
	RefreshToken    string
	ExpiresIn       int64
}

// ExchangeCode 用 code 换取 token
func (m *OAuthManager) ExchangeCode(ctx context.Context, code string) (*TokenResult, error) {
	resp, _, err := m.larkClient.Auth.GetAccessToken(ctx, &lark.GetAccessTokenReq{
		GrantType: "authorization_code",
		Code:      code,
	})
	if err != nil {
		return nil, fmt.Errorf("换取 token 失败: %w", err)
	}

	return &TokenResult{
		UserAccessToken: resp.AccessToken,
		RefreshToken:    resp.RefreshToken,
		ExpiresIn:       resp.ExpiresIn,
	}, nil
}

// RefreshUserToken 刷新 user_access_token
func (m *OAuthManager) RefreshUserToken(ctx context.Context, refreshToken string) (*TokenResult, error) {
	resp, _, err := m.larkClient.Auth.RefreshAccessToken(ctx, &lark.RefreshAccessTokenReq{
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("刷新 token 失败: %w", err)
	}

	return &TokenResult{
		UserAccessToken: resp.AccessToken,
		RefreshToken:    resp.RefreshToken,
		ExpiresIn:       resp.ExpiresIn,
	}, nil
}

// RevokeToken 撤销一枚已签发的 OAuth token（best-effort 登出用）。
// 飞书 SDK 未暴露 revoke API，直接打裸 HTTP：POST <accounts 域>/oauth/v1/revoke，
// application/x-www-form-urlencoded。tokenTypeHint 取 "refresh_token" 或 "access_token"。
func (m *OAuthManager) RevokeToken(ctx context.Context, token, tokenTypeHint string) error {
	form := url.Values{}
	form.Set("client_id", m.appId)
	form.Set("client_secret", m.appSecret)
	form.Set("token", token)
	if tokenTypeHint != "" {
		form.Set("token_type_hint", tokenTypeHint)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.revokeBaseURL+revokePath, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("构造 revoke 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("revoke 失败: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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

// OAuthCallbackServer 本地回调服务器
type OAuthCallbackServer struct {
	port          int
	expectedState string
	codeChan      chan string
	errChan       chan error
	server        *http.Server
}

// NewOAuthCallbackServer 创建回调服务器，expectedState 用于校验回调防 CSRF
func NewOAuthCallbackServer(port int, expectedState string) *OAuthCallbackServer {
	return &OAuthCallbackServer{
		port:          port,
		expectedState: expectedState,
		codeChan:      make(chan string, 1),
		errChan:       make(chan error, 1),
	}
}

// Start 启动回调服务器
func (s *OAuthCallbackServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc(DefaultRedirectPath, s.handleCallback)

	// 只绑 loopback，不暴露到 0.0.0.0（redirect URI 仍是 http://localhost:<port>/callback）
	s.server = &http.Server{
		Addr:    fmt.Sprintf("localhost:%d", s.port),
		Handler: mux,
	}

	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("端口 %d 被占用: %w", s.port, err)
	}

	go func() {
		if err := s.server.Serve(ln); err != http.ErrServerClosed {
			s.errChan <- err
		}
	}()

	return nil
}

// handleCallback 处理 OAuth 回调
func (s *OAuthCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// state 不匹配：CSRF 或本地垃圾请求，拒绝但不打断等待（继续等正确回调直到超时）
	if query.Get("state") != s.expectedState {
		http.Error(w, "state 校验失败", http.StatusBadRequest)
		return
	}

	// 有 error 参数：飞书用户拒绝授权的真实回调，才打断等待
	if errMsg := query.Get("error"); errMsg != "" {
		http.Error(w, "授权失败: "+errMsg, http.StatusBadRequest)
		s.errChan <- fmt.Errorf("授权失败: %s", errMsg)
		return
	}

	code := query.Get("code")
	if code == "" {
		// 无 code 无 error：浏览器预取等噪声请求，不打断等待
		http.Error(w, "无效的回调请求", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>授权成功</title></head>
<body style="text-align:center;padding-top:50px;font-family:sans-serif;">
    <h1>&#10004; 授权成功</h1>
    <p>你可以关闭此页面，返回命令行继续操作。</p>
</body>
</html>`))

	s.codeChan <- code
}

// WaitForCode 等待授权码
func (s *OAuthCallbackServer) WaitForCode(timeout time.Duration) (string, error) {
	select {
	case code := <-s.codeChan:
		return code, nil
	case err := <-s.errChan:
		return "", err
	case <-time.After(timeout):
		return "", fmt.Errorf("等待授权超时（%v）", timeout)
	}
}

// Shutdown 关闭服务器
func (s *OAuthCallbackServer) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
