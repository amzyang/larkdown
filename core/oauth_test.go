package core

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager 构造一个测试用 OAuthManager；调用方按需注入 accountsBaseURL/openBaseURL/sleep。
func newTestManager() *OAuthManager {
	return NewOAuthManager("app-id", "app-secret", nil)
}

// noWait 是消除轮询真实等待的 sleep 注入。
func noWait(context.Context, time.Duration) error { return nil }

func TestRequestDeviceCodeSendsBasicAuthAndScope(t *testing.T) {
	var gotPath, gotContentType, gotMethod, gotAuth string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		w.Write([]byte(`{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://example.com/device","verification_uri_complete":"https://example.com/device?code=WDJB-MJHT","expires_in":300,"interval":5}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.accountsBaseURL = server.URL // 注入 httptest，绝不打真实飞书端点

	res, err := mgr.RequestDeviceCode(context.Background(), "docx:document:readonly")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, deviceAuthorizationPath, gotPath)
	assert.Equal(t, "application/x-www-form-urlencoded", gotContentType)
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("app-id:app-secret")), gotAuth)
	assert.Equal(t, "app-id", gotForm.Get("client_id"))
	assert.Contains(t, gotForm.Get("scope"), "docx:document:readonly")
	assert.Contains(t, gotForm.Get("scope"), "offline_access")

	assert.Equal(t, "dc", res.DeviceCode)
	assert.Equal(t, "WDJB-MJHT", res.UserCode)
	assert.Equal(t, "https://example.com/device", res.VerificationURI)
	assert.Equal(t, "https://example.com/device?code=WDJB-MJHT", res.VerificationURIComplete)
	assert.Equal(t, 300, res.ExpiresIn)
	assert.Equal(t, 5, res.Interval)
}

func TestRequestDeviceCodeDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 省略 expires_in / interval / verification_uri_complete
		w.Write([]byte(`{"device_code":"dc","user_code":"UC","verification_uri":"https://example.com/device"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.accountsBaseURL = server.URL

	res, err := mgr.RequestDeviceCode(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, 240, res.ExpiresIn)
	assert.Equal(t, 5, res.Interval)
	assert.Equal(t, "https://example.com/device", res.VerificationURIComplete) // 回落 verification_uri
}

func TestRequestDeviceCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_client","error_description":"app not found"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.accountsBaseURL = server.URL

	_, err := mgr.RequestDeviceCode(context.Background(), "scope")
	assert.ErrorContains(t, err, "app not found")
}

func TestPollPendingThenSuccess(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		w.Write([]byte(`{"access_token":"u-tok","refresh_token":"r-tok","expires_in":7200,"refresh_token_expires_in":604800,"scope":"docx"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL
	mgr.sleep = noWait

	res, err := mgr.PollDeviceToken(context.Background(), "dc", 5, 240)
	require.NoError(t, err)
	assert.Equal(t, "u-tok", res.UserAccessToken)
	assert.Equal(t, "r-tok", res.RefreshToken)
	assert.Equal(t, int64(7200), res.ExpiresIn)
	assert.Equal(t, int64(604800), res.RefreshExpiresIn)
	assert.Equal(t, 3, calls)
}

func TestPollSlowDownIncreasesInterval(t *testing.T) {
	var calls int
	var gotPath string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath = r.URL.Path
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		if calls == 1 {
			w.Write([]byte(`{"error":"slow_down"}`))
			return
		}
		w.Write([]byte(`{"access_token":"u-tok","refresh_token":"r-tok"}`))
	}))
	defer server.Close()

	var waits []time.Duration
	mgr := newTestManager()
	mgr.openBaseURL = server.URL
	mgr.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	res, err := mgr.PollDeviceToken(context.Background(), "the-device-code", 5, 240)
	require.NoError(t, err)
	assert.Equal(t, "u-tok", res.UserAccessToken)

	assert.Equal(t, tokenV2Path, gotPath)
	assert.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", gotForm.Get("grant_type"))
	assert.Equal(t, "the-device-code", gotForm.Get("device_code"))
	assert.Equal(t, "app-id", gotForm.Get("client_id"))
	assert.Equal(t, "app-secret", gotForm.Get("client_secret"))

	// 首轮等待 5s，slow_down 后升到 10s
	require.Len(t, waits, 2)
	assert.Equal(t, 5*time.Second, waits[0])
	assert.Equal(t, 10*time.Second, waits[1])
}

func TestPollAccessDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"access_denied","error_description":"user denied"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL
	mgr.sleep = noWait

	_, err := mgr.PollDeviceToken(context.Background(), "dc", 5, 240)
	var dfe *DeviceFlowError
	require.ErrorAs(t, err, &dfe)
	assert.Equal(t, "access_denied", dfe.Reason)
}

func TestPollExpiredToken(t *testing.T) {
	for _, errCode := range []string{"expired_token", "invalid_grant"} {
		t.Run(errCode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"error":"` + errCode + `"}`))
			}))
			defer server.Close()

			mgr := newTestManager()
			mgr.openBaseURL = server.URL
			mgr.sleep = noWait

			_, err := mgr.PollDeviceToken(context.Background(), "dc", 5, 240)
			var dfe *DeviceFlowError
			require.ErrorAs(t, err, &dfe)
			assert.Equal(t, "expired_token", dfe.Reason)
		})
	}
}

func TestPollTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL
	mgr.sleep = noWait
	mgr.maxPollAttempts = 3 // 快速触发轮询上界 → timeout 分支

	_, err := mgr.PollDeviceToken(context.Background(), "dc", 5, 240)
	var dfe *DeviceFlowError
	require.ErrorAs(t, err, &dfe)
	assert.Equal(t, "expired_token", dfe.Reason)
	assert.ErrorContains(t, err, "超时")
}

func TestPollSuccessDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 省略 expires_in / refresh_token_expires_in
		w.Write([]byte(`{"access_token":"u-tok","refresh_token":"r-tok"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL
	mgr.sleep = noWait

	res, err := mgr.PollDeviceToken(context.Background(), "dc", 5, 240)
	require.NoError(t, err)
	assert.Equal(t, int64(7200), res.ExpiresIn)
	assert.Equal(t, int64(604800), res.RefreshExpiresIn)
}

func TestRefreshSuccessSendsFormNotBasicAuth(t *testing.T) {
	var gotPath, gotAuth string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		w.Write([]byte(`{"access_token":"u-new","refresh_token":"r-new","expires_in":7200,"refresh_token_expires_in":604800}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL

	res, err := mgr.RefreshUserToken(context.Background(), "r-old")
	require.NoError(t, err)

	assert.Equal(t, tokenV2Path, gotPath)
	assert.Empty(t, gotAuth) // 刷新用 form 里的 client_secret，不用 Basic auth
	assert.Equal(t, "refresh_token", gotForm.Get("grant_type"))
	assert.Equal(t, "r-old", gotForm.Get("refresh_token"))
	assert.Equal(t, "app-id", gotForm.Get("client_id"))
	assert.Equal(t, "app-secret", gotForm.Get("client_secret"))

	assert.Equal(t, "u-new", res.UserAccessToken)
	assert.Equal(t, "r-new", res.RefreshToken)
	assert.Equal(t, int64(7200), res.ExpiresIn)
	assert.Equal(t, int64(604800), res.RefreshExpiresIn)
}

func TestRefreshInvalidGrantIsDeterministic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL

	_, err := mgr.RefreshUserToken(context.Background(), "r-old")
	var rerr *RefreshTokenError
	require.ErrorAs(t, err, &rerr)
	assert.Equal(t, "invalid_grant", rerr.OAuthError)
	assert.Equal(t, http.StatusBadRequest, rerr.HTTPStatus)
	assert.False(t, isTransientRefreshError(err))
}

func TestRefresh5xxIsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"server_error"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL

	_, err := mgr.RefreshUserToken(context.Background(), "r-old")
	require.Error(t, err)
	assert.True(t, isTransientRefreshError(err))
}

func TestRefresh429IsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL

	_, err := mgr.RefreshUserToken(context.Background(), "r-old")
	require.Error(t, err)
	assert.True(t, isTransientRefreshError(err))
}

func TestRefreshFeishuCodeBody(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		transient bool
	}{
		{"确定性失效 code 20037", `{"code":20037,"msg":"expired"}`, false},
		{"瞬时 code 20050", `{"code":20050,"msg":"internal"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body)) // HTTP 200 但 body code != 0
			}))
			defer server.Close()

			mgr := newTestManager()
			mgr.openBaseURL = server.URL

			_, err := mgr.RefreshUserToken(context.Background(), "r-old")
			require.Error(t, err)
			assert.Equal(t, tc.transient, isTransientRefreshError(err))
		})
	}
}

func TestRefreshTokenReuseFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"u-new","expires_in":7200}`)) // 缺 refresh_token
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.openBaseURL = server.URL

	res, err := mgr.RefreshUserToken(context.Background(), "r-old")
	require.NoError(t, err)
	assert.Equal(t, "r-old", res.RefreshToken) // 未轮换，回落入参
}

func TestRevokeTokenSendsFormFields(t *testing.T) {
	var gotPath, gotContentType, gotMethod string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.accountsBaseURL = server.URL // 注入 httptest，绝不打真实飞书端点

	require.NoError(t, mgr.RevokeToken(context.Background(), "the-refresh-token", "refresh_token"))

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, revokePath, gotPath)
	assert.Equal(t, "application/x-www-form-urlencoded", gotContentType)
	assert.Equal(t, "app-id", gotForm.Get("client_id"))
	assert.Equal(t, "app-secret", gotForm.Get("client_secret"))
	assert.Equal(t, "the-refresh-token", gotForm.Get("token"))
	assert.Equal(t, "refresh_token", gotForm.Get("token_type_hint"))
}

func TestRevokeTokenHTTPErrorReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_request", http.StatusBadRequest)
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.accountsBaseURL = server.URL

	err := mgr.RevokeToken(context.Background(), "t", "access_token")
	assert.ErrorContains(t, err, "HTTP 400")
}

func TestRevokeTokenBusinessErrorCode(t *testing.T) {
	// HTTP 200 但 body 里业务 code != 0，也应视为失败
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":20064,"msg":"token revoked"}`))
	}))
	defer server.Close()

	mgr := newTestManager()
	mgr.accountsBaseURL = server.URL

	err := mgr.RevokeToken(context.Background(), "t", "refresh_token")
	assert.ErrorContains(t, err, "20064")
}
