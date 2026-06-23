package core

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chyroc/lark"
	"github.com/stretchr/testify/assert"
)

func TestRateLimiterPerAPIIsolation(t *testing.T) {
	rl := NewRateLimiter(false)

	l1 := rl.getOrCreateLimiter("Drive#GetDocx", "GET")
	l2 := rl.getOrCreateLimiter("Drive#CreateDocxDescendant", "POST")
	l3 := rl.getOrCreateLimiter("Drive#GetDocx", "GET") // 同 key 复用

	assert.Same(t, l1, l3, "同 key 应复用限流器")
	assert.NotSame(t, l1, l2, "不同 key 应独立")

	// GET 限速 5, POST 限速 3
	assert.Equal(t, float64(readRate), float64(l1.Limit()))
	assert.Equal(t, float64(writeRate), float64(l2.Limit()))
}

func TestRateLimiterMiddlewarePassThrough(t *testing.T) {
	rl := NewRateLimiter(false)
	mw := rl.Middleware()

	var called int32
	endpoint := mw(func(ctx context.Context, req *lark.RawRequestReq, resp any) (*lark.Response, error) {
		atomic.AddInt32(&called, 1)
		return &lark.Response{StatusCode: 200}, nil
	})

	resp, err := endpoint(context.Background(), &lark.RawRequestReq{
		Scope:  "Drive",
		API:    "GetDocxDocument",
		Method: "GET",
	}, nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&called))
}

func TestRateLimiterRetryOn429(t *testing.T) {
	rl := NewRateLimiter(true)
	mw := rl.Middleware()

	var attempts int32
	endpoint := mw(func(ctx context.Context, req *lark.RawRequestReq, resp any) (*lark.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			return &lark.Response{
				StatusCode: 429,
				Header:     http.Header{"X-Ogw-Ratelimit-Reset": []string{"1"}},
			}, nil
		}
		return &lark.Response{StatusCode: 200}, nil
	})

	resp, err := endpoint(context.Background(), &lark.RawRequestReq{
		Scope:  "Drive",
		API:    "CreateDocxDescendant",
		Method: "POST",
	}, nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int32(3), atomic.LoadInt32(&attempts))
}

func TestRateLimiterRetryOnErrorCode(t *testing.T) {
	rl := NewRateLimiter(false)
	mw := rl.Middleware()

	var attempts int32
	endpoint := mw(func(ctx context.Context, req *lark.RawRequestReq, resp any) (*lark.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			return &lark.Response{StatusCode: 400}, &lark.Error{Code: rateLimitErrorCode, Msg: "frequency limit"}
		}
		return &lark.Response{StatusCode: 200}, nil
	})

	resp, err := endpoint(context.Background(), &lark.RawRequestReq{
		Scope:  "Drive",
		API:    "BatchUpdateDocxBlock",
		Method: "PATCH",
	}, nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts))
}

func TestRateLimiterMaxRetryExhausted(t *testing.T) {
	rl := NewRateLimiter(false)
	mw := rl.Middleware()

	var attempts int32
	endpoint := mw(func(ctx context.Context, req *lark.RawRequestReq, resp any) (*lark.Response, error) {
		atomic.AddInt32(&attempts, 1)
		return &lark.Response{StatusCode: 429, Header: http.Header{"X-Ogw-Ratelimit-Reset": []string{"0"}}},
			&lark.Error{Code: rateLimitErrorCode}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := endpoint(ctx, &lark.RawRequestReq{
		Scope:  "Drive",
		API:    "CreateDocxDescendant",
		Method: "POST",
	}, nil)

	// 重试 3 次后返回最后一次的结果
	assert.Error(t, err)
	assert.Equal(t, 429, resp.StatusCode)
	assert.Equal(t, int32(maxRetry), atomic.LoadInt32(&attempts))
}

func TestRetryDelayUsesHeader(t *testing.T) {
	resp := &lark.Response{
		Header: http.Header{"X-Ogw-Ratelimit-Reset": []string{"5"}},
	}
	d := retryDelay(resp, 0)
	assert.Equal(t, 5*time.Second, d)
}

func TestRetryDelayFallbackExponential(t *testing.T) {
	d0 := retryDelay(nil, 0)
	d1 := retryDelay(nil, 1)

	// attempt 0: base=1s + jitter(0~200ms) → [1s, 1.2s]
	assert.GreaterOrEqual(t, d0, 1*time.Second)
	assert.Less(t, d0, 1300*time.Millisecond)

	// attempt 1: base=2s + jitter(0~400ms) → [2s, 2.4s]
	assert.GreaterOrEqual(t, d1, 2*time.Second)
	assert.Less(t, d1, 2500*time.Millisecond)
}

func TestRetryDelayCapsAt30s(t *testing.T) {
	d := retryDelay(nil, 10) // 2^10 = 1024s → capped at 30s
	assert.LessOrEqual(t, d, maxDelay)
}

func TestIsRateLimited(t *testing.T) {
	assert.False(t, isRateLimited(nil, nil))
	assert.False(t, isRateLimited(&lark.Response{StatusCode: 200}, nil))
	assert.True(t, isRateLimited(&lark.Response{StatusCode: 429}, nil))
	assert.True(t, isRateLimited(&lark.Response{StatusCode: 400}, &lark.Error{Code: rateLimitErrorCode}))
	assert.False(t, isRateLimited(&lark.Response{StatusCode: 400}, &lark.Error{Code: 12345}))
}

func TestRateLimiterContextCancelled(t *testing.T) {
	rl := NewRateLimiter(false)
	mw := rl.Middleware()

	endpoint := mw(func(ctx context.Context, req *lark.RawRequestReq, resp any) (*lark.Response, error) {
		return &lark.Response{StatusCode: 429, Header: http.Header{"X-Ogw-Ratelimit-Reset": []string{"10"}}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := endpoint(ctx, &lark.RawRequestReq{
		Scope:  "Drive",
		API:    "Test",
		Method: "GET",
	}, nil)

	assert.Error(t, err)
}
