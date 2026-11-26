package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/config"

	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/contextx"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
	"github.com/admin-frame/admin-frame/pkg/idempotency"
	"github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/middleware"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
	"github.com/admin-frame/admin-frame/pkg/redis"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// PipelineConfig 是入口请求链路参数（REL-01 / REL-05）。
type PipelineConfig struct {
	// Timeout 单请求 context deadline（软超时）；≤0 不注入。
	Timeout time.Duration
	// BodyLimitBytes 请求体上限；≤0 不限制。
	BodyLimitBytes int64
	// Channel 本进程端别，用于限流分键。
	Channel string
	// RateLimit 限流参数；Enabled=false 时不挂载。
	RateLimit config.RateLimitConfig
	// RateLimitSource 非空时在运行期覆盖 RateLimit 的 rps / burst / idle_ttl /
	// degrade_on_failure（ADR-0015 热更）；nil 表示只用配置文件静态值。
	RateLimitSource ratelimit.ParamsProvider
	// Idempotency 幂等参数；Enabled=false 时不挂载。
	Idempotency config.IdempotencyConfig
	// IdempotencyTTL 非空时在每次写请求上取当前 TTL，覆盖 Idempotency.TTL
	// （ADR-0015 热更）；只影响新写入的记录。
	IdempotencyTTL func() time.Duration
	// CORS 跨域参数；AllowOrigins 为空时不挂载。
	CORS config.CORSConfig
}

// PipelineDeps 是链路依赖的平台能力。
type PipelineDeps struct {
	Logger  *slog.Logger
	Metrics *metrics.Metrics
	Tracer  *tracing.Provider
	// Redis 共享客户端；限流 / 幂等 driver=redis 时必需。
	Redis *redis.Client
	// ClientIP 是可信代理解析器；nil 表示不信任任何代理头。
	ClientIP *pkghttpx.ClientIPResolver
}

// NewPipeline 按固定顺序装配入口中间件。
//
// 顺序（外 → 内）：
//
//	Recover → Trace → RequestID → Metrics → AccessLog → CORS
//	→ Timeout → BodyLimit → RateLimit → Idempotency → Handler
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
	limited, err := newRateLimit(cfg.RateLimit, cfg.Channel, cfg.RateLimitSource, deps.Redis, deps.ClientIP)
	if err != nil {
		return nil, err
	}
	if limited != nil {
		mws = append(mws, limited)
	}
	idem, err := newIdempotency(cfg.Idempotency, cfg.IdempotencyTTL, deps.Redis)
	if err != nil {
		return nil, err
	}
	if idem != nil {
		mws = append(mws, idem)
	}
	// 必须最内层（紧贴 mux）：ServeMux 只把匹配到的 Pattern 写在传给它的请求上，
	// 外层 Timeout 等中间件复制过请求后就取不到了，span 名会退化成 GET unknown。
	mws = append(mws, tracing.RoutePatternMiddleware())
	return func(next http.Handler) http.Handler {
		return middleware.Chain(next, mws...)
	}, nil
}

// bodyLimit 限制请求体大小：超限直接返回稳定业务码，不发往业务 Handler。
func bodyLimit(limit int64) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID := apiresp.TraceIDFromContext(r.Context())
			if r.ContentLength > limit {
				apiresp.FromError(w, apperr.BadRequest("request body too large"), traceID)
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
//
// source 非空时走运行期可改参路径（ADR-0015）：参数变更只调整既有桶，
// 不重建、不清空配额；source 为 nil 时与改造前完全一致。
func newRateLimit(
	cfg config.RateLimitConfig,
	channel string,
	source ratelimit.ParamsProvider,
	redisClient *redis.Client,
	clientIP *pkghttpx.ClientIPResolver,
) (middleware.Middleware, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.RPS <= 0 || cfg.Burst < 1 {
		return nil, errors.New("platform: rate_limit requires rps > 0 and burst >= 1")
	}
	var buckets middleware.Buckets
	switch cfg.Driver {
	case "", config.RateLimitDriverMemory:
		if source != nil {
			adaptive, err := ratelimit.NewAdaptive(source, ratelimit.WithAdaptiveIdleTTL(cfg.IdleTTL))
			if err != nil {
				return nil, fmt.Errorf("platform: adaptive rate limiter: %w", err)
			}
			buckets = adaptive
			break
		}
		keyed, err := ratelimit.NewKeyed(
			func() (ratelimit.Limiter, error) {
				return ratelimit.NewTokenBucket(cfg.RPS, cfg.Burst)
			},
			ratelimit.WithIdleTTL(cfg.IdleTTL),
		)
		if err != nil {
			return nil, fmt.Errorf("platform: rate limiter: %w", err)
		}
		buckets = keyed
	case config.RateLimitDriverRedis:
		shared, err := ratelimit.NewRedisKeyed(redisClient, ratelimit.RedisKeyedConfig{
			RPS:              cfg.RPS,
			Burst:            cfg.Burst,
			IdleTTL:          cfg.IdleTTL,
			FailureMode:      cfg.FailureMode,
			DegradeOnFailure: cfg.DegradeOnFailure,
		})
		if err != nil {
			return nil, fmt.Errorf("platform: redis rate limiter: %w", err)
		}
		if source != nil {
			buckets = &redisHotBuckets{inner: shared, source: source}
		} else {
			buckets = shared
		}
	default:
		return nil, fmt.Errorf("platform: unsupported rate_limit.driver %q", cfg.Driver)
	}
	return middleware.RateLimit(middleware.RateLimitConfig{
		Buckets: buckets,
		KeyFunc: rateLimitKeyFunc(cfg.Key, channel, clientIP),
		OnLimited: func(w http.ResponseWriter, r *http.Request, result ratelimit.Result) {
			if result.RetryAfter > 0 {
				w.Header().Set("Retry-After", middleware.RetryAfterSeconds(result.RetryAfter))
			}
			apiresp.FromError(w, apperr.TooManyRequests("too many requests"), apiresp.TraceIDFromContext(r.Context()))
		},
	}), nil
}

// redisHotBuckets 把热更参数下发给 Redis 限流器（ADR-0015）。
//
// Redis 侧参数逐次进 Lua，因此只需在版本变化时调一次 Configure；
// 快路径用 atomic 比较版本，不取锁。
type redisHotBuckets struct {
	inner   *ratelimit.RedisKeyed
	source  ratelimit.ParamsProvider
	version atomic.Int64
	applyMu sync.Mutex
	inited  atomic.Bool
}

// Allow 同步参数后放行。
func (b *redisHotBuckets) Allow(key string) ratelimit.Result {
	b.syncParams()
	return b.inner.Allow(key)
}

// syncParams 在参数版本变化时重配内部限流器；读取失败时保留既有参数。
func (b *redisHotBuckets) syncParams() {
	params, version, ok := b.source.Params()
	if !ok || params.RPS <= 0 || params.Burst < 1 {
		return
	}
	if b.inited.Load() && b.version.Load() == version {
		return
	}
	b.applyMu.Lock()
	defer b.applyMu.Unlock()
	if b.inited.Load() && b.version.Load() == version {
		return
	}
	if err := b.inner.Configure(params); err != nil {
		return
	}
	b.version.Store(version)
	b.inited.Store(true)
}

// newIdempotency 按配置构造幂等中间件；存储不可用失败关闭。
func newIdempotency(cfg config.IdempotencyConfig, ttlSource func() time.Duration,
	redisClient *redis.Client) (middleware.Middleware, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	var store idempotency.Store
	switch cfg.Driver {
	case "", config.IdempotencyDriverMemory:
		store = idempotency.NewMemoryStore()
	case config.IdempotencyDriverRedis:
		shared, err := idempotency.NewRedisStore(redisClient)
		if err != nil {
			return nil, fmt.Errorf("platform: redis idempotency store: %w", err)
		}
		store = shared
	default:
		return nil, fmt.Errorf("platform: unsupported idempotency.driver %q", cfg.Driver)
	}
	return idempotency.Middleware(idempotency.Config{
		Store:          store,
		TTL:            cfg.TTL,
		TTLSource:      ttlSource,
		Header:         cfg.Header,
		Required:       cfg.Required,
		MaxBodyBytes:   cfg.MaxBodyBytes,
		DegradeOnError: cfg.DegradeOnError,
		OnError: func(w http.ResponseWriter, r *http.Request, err error) {
			apiresp.FromError(w, err, apiresp.TraceIDFromContext(r.Context()))
		},
	}), nil
}

// rateLimitKeyFunc 按策略生成限流键；身份未建立时回退客户端 IP，避免匿名流量共享单桶。
func rateLimitKeyFunc(strategy, channel string, resolver *pkghttpx.ClientIPResolver) func(*http.Request) string {
	return func(r *http.Request) string {
		ip := resolver.ClientIP(r)
		identity := contextx.IdentityFrom(r.Context())
		switch strategy {
		case config.RateLimitKeyClient:
			return ip
		case config.RateLimitKeyChannelUser:
			if identity.UserID != "" {
				return channel + "|" + identity.UserID
			}
			return channel + "|" + ip
		case config.RateLimitKeyChannelTenantUser:
			if identity.UserID != "" {
				return channel + "|" + identity.MerchantID + "|" + identity.UserID
			}
			return channel + "|" + ip
		default:
			return channel
		}
	}
}
