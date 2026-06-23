package core

import (
	"context"
	"log"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"github.com/chyroc/lark"
	"golang.org/x/time/rate"
)

const (
	readRate  = 5 // GET 请求：5 req/s
	writeRate = 3 // 写操作（POST/PATCH/DELETE）：3 req/s，匹配等级 21
	maxRetry  = 3
	maxDelay  = 30 * time.Second

	rateLimitErrorCode = 99991400
)

// RateLimiter 实现 per-API 主动限流 + 响应式重试。
// 以 Scope#API 为 key 维护独立限流器，根据 HTTP method 区分速率。
type RateLimiter struct {
	limiters sync.Map // map[string]*rate.Limiter
	debug    bool
}

func NewRateLimiter(debug bool) *RateLimiter {
	return &RateLimiter{debug: debug}
}

func (rl *RateLimiter) getOrCreateLimiter(key, method string) *rate.Limiter {
	if v, ok := rl.limiters.Load(key); ok {
		return v.(*rate.Limiter)
	}
	r := rate.Limit(readRate)
	if method != "GET" {
		r = writeRate
	}
	l := rate.NewLimiter(r, int(r))
	actual, _ := rl.limiters.LoadOrStore(key, l)
	return actual.(*rate.Limiter)
}

// Middleware 返回 lark.ApiMiddleware，集成主动限流和响应式重试。
func (rl *RateLimiter) Middleware() lark.ApiMiddleware {
	return func(next lark.ApiEndpoint) lark.ApiEndpoint {
		return func(ctx context.Context, req *lark.RawRequestReq, resp interface{}) (*lark.Response, error) {
			key := req.Scope + "#" + req.API
			limiter := rl.getOrCreateLimiter(key, req.Method)

			for attempt := 0; ; attempt++ {
				if err := limiter.Wait(ctx); err != nil {
					return nil, err
				}

				response, err := next(ctx, req, resp)

				if !isRateLimited(response, err) || attempt >= maxRetry-1 {
					return response, err
				}

				delay := retryDelay(response, attempt)
				if rl.debug {
					log.Printf("[DEBUG] [rate_limiter] %s rate limited, retry %d/%d after %s",
						key, attempt+1, maxRetry, delay)
				}

				select {
				case <-ctx.Done():
					return response, ctx.Err()
				case <-time.After(delay):
				}
			}
		}
	}
}

// isRateLimited 判断响应是否为限流错误。
// 飞书限流表现为 HTTP 429 或 HTTP 400 + error code 99991400。
func isRateLimited(resp *lark.Response, err error) bool {
	if resp != nil && resp.StatusCode == 429 {
		return true
	}
	if err != nil {
		if larkErr, ok := err.(*lark.Error); ok && larkErr.Code == rateLimitErrorCode {
			return true
		}
	}
	return false
}

// retryDelay 计算重试等待时间。
// 优先使用飞书推荐的 x-ogw-ratelimit-reset 响应头，
// 否则使用 exponential backoff + jitter。
func retryDelay(resp *lark.Response, attempt int) time.Duration {
	if resp != nil {
		if resetStr := resp.Header.Get("x-ogw-ratelimit-reset"); resetStr != "" {
			if secs, err := strconv.Atoi(resetStr); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	base := time.Duration(1<<uint(attempt)) * time.Second
	jitter := time.Duration(rand.Int64N(int64(base) / 5))
	d := base + jitter
	if d > maxDelay {
		d = maxDelay
	}
	return d
}
