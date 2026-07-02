package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testExpectedState = "expected-state"

func newTestCallbackServer() *OAuthCallbackServer {
	return NewOAuthCallbackServer(DefaultOAuthPort, testExpectedState)
}

func callbackRequest(s *OAuthCallbackServer, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, DefaultRedirectPath+"?"+query, nil)
	w := httptest.NewRecorder()
	s.handleCallback(w, req)
	return w
}

func TestHandleCallbackStateMatchWithCode(t *testing.T) {
	s := newTestCallbackServer()

	w := callbackRequest(s, "code=abc&state="+testExpectedState)
	assert.Equal(t, http.StatusOK, w.Code)

	code, err := s.WaitForCode(50 * time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, "abc", code)
}

func TestHandleCallbackStateMismatchDoesNotInterrupt(t *testing.T) {
	s := newTestCallbackServer()

	// state 不匹配：400 拒绝，但不打断登录等待（否则任意本地垃圾请求可 DoS）
	w := callbackRequest(s, "code=abc&state=wrong")
	assert.Equal(t, http.StatusBadRequest, w.Code)

	_, err := s.WaitForCode(50 * time.Millisecond)
	assert.ErrorContains(t, err, "超时")
}

func TestHandleCallbackUserDeniedGoesToErrChan(t *testing.T) {
	s := newTestCallbackServer()

	// 有 error 参数且 state 匹配：飞书用户拒绝授权的真实回调，才打断等待
	w := callbackRequest(s, "error=access_denied&state="+testExpectedState)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	_, err := s.WaitForCode(50 * time.Millisecond)
	assert.ErrorContains(t, err, "access_denied")
}

func TestHandleCallbackNoCodeNoErrorDoesNotInterrupt(t *testing.T) {
	s := newTestCallbackServer()

	// state 匹配但无 code 无 error（浏览器预取、乱扫）：400 且不打断等待
	w := callbackRequest(s, "state="+testExpectedState)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	_, err := s.WaitForCode(50 * time.Millisecond)
	assert.ErrorContains(t, err, "超时")
}

func TestCallbackServerBindsLoopbackOnly(t *testing.T) {
	// 回调服务器只绑 loopback，不暴露到 0.0.0.0
	s := NewOAuthCallbackServer(0, testExpectedState)
	require.NoError(t, s.Start())
	defer s.Shutdown(context.Background())

	assert.Equal(t, "localhost:0", s.server.Addr)
}
