package middleware

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// RateLimitConfig 配置限流中间件。
type RateLimitConfig struct {
	// Keyed 按 key 分桶的限流器；与 Limiter 二选一（优先 Keyed）。
	Keyed *ratelimit.Keyed
	// Limiter 全局单桶；Keyed 非空时忽略。
	Limiter ratelimit.Limiter
	// KeyFunc 从请求生成 key；nil 时用空串（全局）。
	KeyFunc func(*http.Request) string
	// OnLimited 被限流时的响应；nil 时写 429 + Retry-After（无业务码信封）。
	OnLimited func(w http.ResponseWriter, r *http.Request, result ratelimit.Result)
}

// RateLimit 在放行前扣除配额；拒绝时调用 OnLimited（REL-05）。
func RateLimit(cfg RateLimitConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res := allow(cfg, r)
			if res.Allowed {
				next.ServeHTTP(w, r)
				return
			}
			if cfg.OnLimited != nil {
				cfg.OnLimited(w, r, res)
				return
			}
			writeLimited(w, r, res)
		})
	}
}

func allow(cfg RateLimitConfig, r *http.Request) ratelimit.Result {
	if cfg.Keyed != nil {
		key := ""
		if cfg.KeyFunc != nil {
			key = cfg.KeyFunc(r)
		}
		return cfg.Keyed.Allow(key)
	}
	if cfg.Limiter != nil {
		return cfg.Limiter.Allow()
	}
	return ratelimit.Result{Allowed: true}
}

func writeLimited(w http.ResponseWriter, _ *http.Request, res ratelimit.Result) {
	if res.RetryAfter > 0 {
		sec := int(math.Ceil(res.RetryAfter.Seconds()))
		if sec < 1 {
			sec = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(sec))
	}
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

// DefaultRateLimitKey 示例键：channel + user + merchant + IP（调用方可替换）。
func DefaultRateLimitKey(r *http.Request) string {
	id := contextx.IdentityFrom(r.Context())
	ip := r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip = xff
	}
	return id.Channel + "|" + id.UserID + "|" + id.MerchantID + "|" + ip
}

// RetryAfterSeconds 将时长转为 Retry-After 秒数字符串。
func RetryAfterSeconds(d time.Duration) string {
	if d <= 0 {
		return "1"
	}
	sec := int(math.Ceil(d.Seconds()))
	if sec < 1 {
		sec = 1
	}
	return strconv.Itoa(sec)
}
