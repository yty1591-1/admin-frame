package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/middleware"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// PipelineConfig 是入口请求链路参数（REL-01 / REL-05）。
type PipelineConfig struct {
	// Timeout 单请求 context deadline（软超时）；≤0 不注入。
	Timeout time.Duration
	// BodyLimitBytes 请求体上限；≤0 不限制。
	BodyLimitBytes int64
	// RateLimit 限流参数；Enabled=false 时不挂载。
	RateLimit RateLimitConfig
	// CORS 跨域参数；AllowOrigins 为空时不挂载。
	CORS CORSConfig
}

// PipelineDeps 是链路依赖的平台能力。
type PipelineDeps struct {
	Logger  *slog.Logger
	Metrics *metrics.Metrics
	Tracer  *tracing.Provider
}

// NewPipeline 按固定顺序装配入口中间件。
//
// 顺序（外 → 内）：
//
//	Recover → Trace → RequestID → Metrics → AccessLog → CORS
//	→ Timeout → BodyLimit → RateLimit → Handler
//
// Recover 在最外层，保证任何 panic 都转成统一信封；TraceID 必须在 RequestID
// 之前，RequestID 才能复用 OTel TraceID（OBS-05）。
func NewPipeline(cfg PipelineConfig, deps PipelineDeps) (func(http.Handler) http.Handler, error) {
	mws := make([]middleware.Middleware, 0, 10)
	mws = append(mws, Recover(deps.Logger))
	if deps.Tracer != nil {
		mws = append(mws, tracing.HTTPMiddleware(tracing.WithTracer(deps.Tracer.Tracer("http.server"))))
	}
	mws = append(mws, RequestID())
	if deps.Metrics != nil {
		mws = append(mws, deps.Metrics.HTTP().Middleware())
	}
	mws = append(mws, AccessLog(deps.Logger))
	if len(cfg.CORS.AllowOrigins) > 0 {
		mws = append(mws, middleware.CORS(middleware.CORSConfig{
			AllowOrigins:     cfg.CORS.AllowOrigins,
			AllowHeaders:     cfg.CORS.AllowHeaders,
			ExposeHeaders:    cfg.CORS.ExposeHeaders,
			AllowCredentials: cfg.CORS.AllowCredentials,
			MaxAge:           cfg.CORS.MaxAge,
		}))
	}
	if cfg.Timeout > 0 {
		mws = append(mws, middleware.Timeout(cfg.Timeout))
	}
	if cfg.BodyLimitBytes > 0 {
		mws = append(mws, bodyLimit(cfg.BodyLimitBytes))
	}
	limited, err := newRateLimit(cfg.RateLimit)
	if err != nil {
		return nil, err
	}
	if limited != nil {
		mws = append(mws, limited)
	}
	return func(next http.Handler) http.Handler {
		return middleware.Chain(next, mws...)
	}, nil
}

// bodyLimit 限制请求体大小：超限直接返回稳定业务码，不发往业务 Handler。
func bodyLimit(limit int64) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := httpx.TraceIDFromContext(r.Context())
			if r.ContentLength > limit {
				httpx.FromError(w, apperr.BadRequest("request body too large"), traceID)
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// newRateLimit 构造按调用方分桶的令牌桶限流中间件。
func newRateLimit(cfg RateLimitConfig) (middleware.Middleware, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.RPS <= 0 || cfg.Burst < 1 {
		return nil, errors.New("platform: rate_limit requires rps > 0 and burst >= 1")
	}
	rps, burst := cfg.RPS, cfg.Burst
	keyed, err := ratelimit.NewKeyed(
		func() (ratelimit.Limiter, error) {
			return ratelimit.NewTokenBucket(rps, burst)
		},
		ratelimit.WithIdleTTL(cfg.IdleTTL),
	)
	if err != nil {
		return nil, fmt.Errorf("platform: rate limiter: %w", err)
	}
	return middleware.RateLimit(middleware.RateLimitConfig{
		Keyed:   keyed,
		KeyFunc: middleware.DefaultRateLimitKey,
		OnLimited: func(w http.ResponseWriter, r *http.Request, result ratelimit.Result) {
			if result.RetryAfter > 0 {
				w.Header().Set("Retry-After", middleware.RetryAfterSeconds(result.RetryAfter))
			}
			httpx.FromError(w, apperr.TooManyRequests("too many requests"), httpx.TraceIDFromContext(r.Context()))
		},
	}), nil
}
