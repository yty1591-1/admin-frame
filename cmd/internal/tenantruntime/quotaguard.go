package tenantruntime

import (
	"context"
	"fmt"
	"net/http"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// AuthGuards 按显式顺序返回 B 端鉴权守卫：租户状态 → API 配额。
//
// 顺序即装配契约：已停用租户在租户状态守卫就被 403，不应再消耗配额判定。
// 内存模式（单进程形态）两者均不启用，返回 nil（与旧构造器同口径）。
func AuthGuards(deps *platform.Deps) ([]func(http.Handler) http.Handler, error) {
	tenantMW, err := NewGuard(deps)
	if err != nil {
		return nil, fmt.Errorf("tenant guard: %w", err)
	}
	if tenantMW == nil {
		return nil, nil
	}
	quotaMW, err := NewQuotaGuard(deps)
	if err != nil {
		return nil, fmt.Errorf("quota guard: %w", err)
	}
	guards := []func(http.Handler) http.Handler{tenantMW}
	if quotaMW != nil {
		guards = append(guards, quotaMW)
	}
	return guards, nil
}

// NewQuotaGuard 构造 B 端 API 配额守卫中间件（ENT-14）。
//
// 内存模式（db.driver=memory）返回 nil 表示不启用——与 NewGuard 同口径，
// 单进程形态没有「多副本共享配额」的语义前提。生产与集成环境配置了
// Redis 则走共享令牌桶（键级参数扣减），否则进程内执行（单副本正确）。
func NewQuotaGuard(deps *platform.Deps) (func(http.Handler) http.Handler, error) {
	if deps == nil || deps.Config == nil || deps.Database == nil {
		return nil, nil
	}
	store, _, err := buildDeps(deps)
	if err != nil {
		return nil, err
	}
	var backend app.QuotaBackend
	if deps.Redis != nil {
		shared, rErr := ratelimit.NewRedisKeyed(deps.Redis, ratelimit.RedisKeyedConfig{
			KeyPrefix: "quota:",
			// RPS / Burst 在键级参数传入（每租户异容量），这里只占位过校验。
			RPS:   1,
			Burst: 1,
		})
		if rErr != nil {
			return nil, fmt.Errorf("quota guard redis backend: %w", rErr)
		}
		backend = redisQuotaBackend{inner: shared}
	}
	enforcer, err := app.NewQuotaEnforcer(store.quotas, app.QuotaEnforcerOptions{Backend: backend})
	if err != nil {
		return nil, fmt.Errorf("quota enforcer: %w", err)
	}
	adapter := quotaGuardAdapter{enforcer: enforcer}
	return httpserver.QuotaGuardMiddleware(adapter.AllowRequest), nil
}

// redisQuotaBackend 把 RedisKeyed 的键级参数执行适配为 QuotaBackend。
type redisQuotaBackend struct {
	inner *ratelimit.RedisKeyed
}

// AllowN 实现 app.QuotaBackend：委托 Redis 键级参数扣减。
func (b redisQuotaBackend) AllowN(key string, rps float64, burst, n int) ratelimit.Result {
	return b.inner.AllowNWithParams(key, rps, burst, n)
}

// quotaGuardAdapter 把执行器适配为平台中间件需要的极简函数语义：
// 每请求扣减 1 次，重试等待秒数向上取整。
type quotaGuardAdapter struct {
	enforcer *app.QuotaEnforcer
}

// AllowRequest 实现配额的每请求判定（由平台中间件调用）。
func (g quotaGuardAdapter) AllowRequest(ctx context.Context, tenantID string) (int, error) {
	result, err := g.enforcer.Allow(ctx, tenantID, app.ResourceAPIRPS, "", 1)
	if err != nil {
		// 42900 明确拒绝（执行器内部故障已 fail-open，不会到这里）。
		return retrySeconds(result), nil
	}
	return 0, nil
}

// retrySeconds 从限流结果取建议等待秒数（向上取整，至少 1）。
func retrySeconds(result ratelimit.Result) int {
	if result.RetryAfter <= 0 {
		return 1
	}
	seconds := int(result.RetryAfter.Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}
