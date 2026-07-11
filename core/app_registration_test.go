package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRegistrar 构造测试用 AppRegistrar；调用方按需注入 accountsBaseURL/openBaseURL/sleep。
func newTestRegistrar() *AppRegistrar {
	return NewAppRegistrar(nil)
}

func TestBeginAppRegistrationSendsAnonymousForm(t *testing.T) {
	var gotPath, gotContentType, gotMethod, gotAuth string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		w.Write([]byte(`{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://example.com/page/cli","verification_uri_complete":"https://example.com/page/cli?user_code=WDJB-MJHT","expires_in":300,"interval":5}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL // 注入 httptest，绝不打真实飞书端点

	res, err := reg.BeginAppRegistration(context.Background())
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, appRegistrationPath, gotPath)
	assert.Equal(t, "application/x-www-form-urlencoded", gotContentType)
	assert.Empty(t, gotAuth) // 匿名 device flow，无 Authorization 头
	assert.Equal(t, "begin", gotForm.Get("action"))
	assert.Equal(t, "PersonalAgent", gotForm.Get("archetype"))
	assert.Equal(t, "client_secret", gotForm.Get("auth_method"))
	assert.Equal(t, "open_id tenant_brand", gotForm.Get("request_user_info"))

	assert.Equal(t, "dc", res.DeviceCode)
	assert.Equal(t, "WDJB-MJHT", res.UserCode)
	assert.Equal(t, "https://example.com/page/cli", res.VerificationURI)
	assert.Equal(t, "https://example.com/page/cli?user_code=WDJB-MJHT", res.VerificationURIComplete)
	assert.Equal(t, 300, res.ExpiresIn)
	assert.Equal(t, 5, res.Interval)
}

func TestBeginAppRegistrationDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 省略 expires_in / interval / verification_uri_complete
		w.Write([]byte(`{"device_code":"dc","user_code":"UC","verification_uri":"https://example.com/page/cli"}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL
	reg.openBaseURL = "https://open.example.com"

	res, err := reg.BeginAppRegistration(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 300, res.ExpiresIn)
	assert.Equal(t, 5, res.Interval)
	// complete 缺省时按 open 域拼授权页链接
	assert.Equal(t, "https://open.example.com/page/cli?user_code=UC", res.VerificationURIComplete)
}

func TestBeginAppRegistrationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_request","error_description":"registration disabled"}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL

	_, err := reg.BeginAppRegistration(context.Background())
	assert.ErrorContains(t, err, "registration disabled")
}

func TestPollAppRegistrationPendingThenSuccess(t *testing.T) {
	var calls int
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		if calls <= 2 {
			w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		w.Write([]byte(`{"client_id":"cli_new","client_secret":"sec_new","user_info":{"open_id":"ou_x","tenant_brand":"feishu"}}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL
	reg.sleep = noWait

	creds, err := reg.PollAppRegistration(context.Background(), "the-device-code", 5, 300)
	require.NoError(t, err)

	assert.Equal(t, "poll", gotForm.Get("action"))
	assert.Equal(t, "the-device-code", gotForm.Get("device_code"))
	assert.Equal(t, "cli_new", creds.AppId)
	assert.Equal(t, "sec_new", creds.AppSecret)
	assert.Equal(t, "ou_x", creds.OpenID)
	assert.Equal(t, "feishu", creds.TenantBrand)
	assert.Equal(t, 3, calls)
}

func TestPollAppRegistrationSlowDown(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"error":"slow_down"}`))
			return
		}
		w.Write([]byte(`{"client_id":"cli_new","client_secret":"sec_new"}`))
	}))
	defer server.Close()

	var waits []time.Duration
	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL
	reg.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	creds, err := reg.PollAppRegistration(context.Background(), "dc", 5, 300)
	require.NoError(t, err)
	assert.Equal(t, "cli_new", creds.AppId)

	// 首轮等待 5s，slow_down 后升到 10s
	require.Len(t, waits, 2)
	assert.Equal(t, 5*time.Second, waits[0])
	assert.Equal(t, 10*time.Second, waits[1])
}

func TestPollAppRegistrationAccessDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"access_denied","error_description":"user denied"}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL
	reg.sleep = noWait

	_, err := reg.PollAppRegistration(context.Background(), "dc", 5, 300)
	var dfe *DeviceFlowError
	require.ErrorAs(t, err, &dfe)
	assert.Equal(t, "access_denied", dfe.Reason)
}

func TestPollAppRegistrationExpired(t *testing.T) {
	for _, errCode := range []string{"expired_token", "invalid_grant"} {
		t.Run(errCode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"error":"` + errCode + `"}`))
			}))
			defer server.Close()

			reg := newTestRegistrar()
			reg.accountsBaseURL = server.URL
			reg.sleep = noWait

			_, err := reg.PollAppRegistration(context.Background(), "dc", 5, 300)
			var dfe *DeviceFlowError
			require.ErrorAs(t, err, &dfe)
			assert.Equal(t, "expired_token", dfe.Reason)
		})
	}
}

func TestPollAppRegistrationLarkTenantUnsupported(t *testing.T) {
	// Lark 海外租户：poll 成功但 secret 为空（需切 larksuite 域重 poll，larkdown 不支持）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"client_id":"cli_new","client_secret":"","user_info":{"open_id":"ou_x","tenant_brand":"lark"}}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL
	reg.sleep = noWait

	_, err := reg.PollAppRegistration(context.Background(), "dc", 5, 300)
	require.ErrorIs(t, err, ErrLarkTenantNotSupported)
}

func TestPollAppRegistrationMissingSecretGenericError(t *testing.T) {
	// secret 因其他原因缺失（非 lark 租户）→ 普通错误，不误报 lark 提示
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"client_id":"cli_new","client_secret":"","user_info":{"open_id":"ou_x","tenant_brand":"feishu"}}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL
	reg.sleep = noWait

	_, err := reg.PollAppRegistration(context.Background(), "dc", 5, 300)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrLarkTenantNotSupported)
	var dfe *DeviceFlowError
	assert.False(t, errors.As(err, &dfe))
}

func TestPollAppRegistrationTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer server.Close()

	reg := newTestRegistrar()
	reg.accountsBaseURL = server.URL
	reg.sleep = noWait
	reg.maxPollAttempts = 3 // 快速触发轮询上界 → timeout 分支

	_, err := reg.PollAppRegistration(context.Background(), "dc", 5, 300)
	var dfe *DeviceFlowError
	require.ErrorAs(t, err, &dfe)
	assert.Equal(t, "expired_token", dfe.Reason)
	assert.ErrorContains(t, err, "超时")
}
