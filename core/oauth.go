package core

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/chyroc/lark"
)

const (
	DefaultOAuthPort    = 9999
	DefaultRedirectPath = "/callback"
)

// OAuthManager 管理 OAuth 流程
type OAuthManager struct {
	appId       string
	appSecret   string
	redirectURI string
	larkClient  *lark.Lark
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
		appId:       appId,
		appSecret:   appSecret,
		redirectURI: redirectURI,
		larkClient:  lark.New(larkOpts...),
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

// OAuthCallbackServer 本地回调服务器
type OAuthCallbackServer struct {
	port     int
	codeChan chan string
	errChan  chan error
	server   *http.Server
}

// NewOAuthCallbackServer 创建回调服务器
func NewOAuthCallbackServer(port int) *OAuthCallbackServer {
	return &OAuthCallbackServer{
		port:     port,
		codeChan: make(chan string, 1),
		errChan:  make(chan error, 1),
	}
}

// Start 启动回调服务器
func (s *OAuthCallbackServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc(DefaultRedirectPath, s.handleCallback)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
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
	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := r.URL.Query().Get("error")
		if errMsg == "" {
			errMsg = "未知错误"
		}
		http.Error(w, "授权失败: "+errMsg, http.StatusBadRequest)
		s.errChan <- fmt.Errorf("授权失败: %s", errMsg)
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
