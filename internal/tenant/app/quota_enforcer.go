package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// ResourceAPIRPS 是平台约定的 API 限流型配额资源键：值为每秒请求数，
// 以令牌桶执行（ENT-14）；其余资源键按计数型处理（静态校验）。
const ResourceAPIRPS = "api.rps"

// limitCacheTTL 是有效上限的进程内缓存时长：rps 路径每请求执行，
// 直查库会把配额表变成热点；配额变更最迟 TTL 后生效（可接受，
// 变更有审计可追溯）。
const limitCacheTTL = 30 * time.Second

// QuotaBackend 是配额扣减后端：进程内（单副本语义）或 Redis（多副本共享）。
//
// 容量按租户异构（每租户 limit 不同），因此 rps / burst 走键级参数而非
// 构造期固定——见 pkg/ratelimit.RedisKeyed.AllowNWithParams。
type QuotaBackend interface {
	// AllowN 对 key 以 (rps, burst) 参数原子扣减 n。
	AllowN(key string, rps float64, burst int, n int) ratelimit.Result
}

// memoryQuotaBackend 是进程内后端：按 key 缓存令牌桶，limit 变更时重建。
// 单测与单进程部署形态用；多副本部署必须装配 Redis 后端。
type memoryQuotaBackend struct {
	mu      sync.Mutex
	buckets map[string]bucketEntry
}

type bucketEntry struct {
	limiter ratelimit.Limiter
	rps     float64
	burst   int
}

// AllowN 实现 QuotaBackend：委托进程内令牌桶。
func (m *memoryQuotaBackend) AllowN(key string, rps float64, burst, n int) ratelimit.Result {
	m.mu.Lock()
	entry, ok := m.buckets[key]
	if !ok || entry.rps != rps || entry.burst != burst {
		limiter, err := ratelimit.NewTokenBucket(rps, burst)
		if err != nil {
			m.mu.Unlock()
			// 参数非法：与 Redis 后端 fail-open 同语义。
			return ratelimit.Result{Allowed: true}
		}
		entry = bucketEntry{limiter: limiter, rps: rps, burst: burst}
		if m.buckets == nil {
			m.buckets = make(map[string]bucketEntry)
		}
		m.buckets[key] = entry
	}
	m.mu.Unlock()
	return entry.limiter.AllowN(n)
}

// QuotaEnforcer 执行租户配额（ENT-14）：把 tenant_quota 的「上限配置」
// 变成运行时拒绝——限流型资源（*.rps）按分键令牌桶扣减，计数型资源做
// 静态校验；超限统一 42900（apperr.TooManyRequests）。
//
// 口径（与 T-13 席位执行一致）：
//   - 配额行缺失且无默认值 → 不限（套餐物化保证正式租户都有行）；
//   - limit = QuotaUnlimited(-1) → 不限；
//   - fail-open：配额读取或后端故障时放行——配额执行不应比业务本身
//     更脆（Redis 后端可用 failure_mode=fail_closed 收紧）；
//   - 计数型资源的用量推进仍由业务事务承担（如席位），本执行器提供
//     Check 静态校验供其复用。
type QuotaEnforcer struct {
	quotas   domain.QuotaRepository
	backend  QuotaBackend
	defaults map[string]int64
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]limitEntry
}

type limitEntry struct {
	limit     int64
	fetchedAt time.Time
}

// QuotaEnforcerOptions 构造参数。
type QuotaEnforcerOptions struct {
	// Defaults 资源键 → 缺行兜底上限；未登记的资源键缺行视为不限。
	// 计数型资源（如 user）建议显式登记，防止漏配套餐时静默放开。
	Defaults map[string]int64
	// Backend 配额扣减后端；nil 时使用进程内实现（单副本语义）。
	Backend QuotaBackend
	// Now 时钟；测试注入。
	Now func() time.Time
}

// NewQuotaEnforcer 构造执行器；配额仓储必填。
func NewQuotaEnforcer(quotas domain.QuotaRepository, opts QuotaEnforcerOptions) (*QuotaEnforcer, error) {
	if quotas == nil {
		return nil, apperr.Internal("quota enforcer requires a quota repository", nil)
	}
	backend := opts.Backend
	if backend == nil {
		backend = &memoryQuotaBackend{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &QuotaEnforcer{
		quotas:   quotas,
		backend:  backend,
		defaults: opts.Defaults,
		now:      now,
		cache:    make(map[string]limitEntry),
	}, nil
}

// Limit 解析租户对某资源的有效上限：配额行 → 默认值 → 不限。
// 结果带 TTL 缓存；仓储故障 fail-open（返回不限）。
func (e *QuotaEnforcer) Limit(ctx context.Context, tenantID, resource string) int64 {
	key := cleanKeySegment(tenantID) + ":" + cleanKeySegment(resource)
	now := e.now()

	e.mu.Lock()
	if entry, ok := e.cache[key]; ok && now.Sub(entry.fetchedAt) < limitCacheTTL {
		e.mu.Unlock()
		return entry.limit
	}
	e.mu.Unlock()

	limit, ok := e.resolve(ctx, tenantID, resource)
	if !ok {
		// 仓储故障：本次放行，不缓存（下次重试）。
		return domain.QuotaUnlimited
	}

	e.mu.Lock()
	e.cache[key] = limitEntry{limit: limit, fetchedAt: now}
	e.mu.Unlock()
	return limit
}

// resolve 查库解析上限；第二返回值 false 表示仓储故障（fail-open）。
func (e *QuotaEnforcer) resolve(ctx context.Context, tenantID, resource string) (int64, bool) {
	quotas, err := e.quotas.ListQuotas(ctx, tenantID)
	if err != nil {
		return domain.QuotaUnlimited, false
	}
	for _, quota := range quotas {
		if quota.Resource == resource {
			return quota.Limit, true
		}
	}
	if limit, ok := e.defaults[resource]; ok {
		return limit, true
	}
	return domain.QuotaUnlimited, true
}

// Allow 限流型执行：对 (tenant, resource, dimension) 分键扣减 n 次配额。
// dimension 参与分键（如用户 ID / 接口名；空 = 租户粒度）。
//
// 超限返回 42900；后端故障默认 fail-open（与 pkg/ratelimit Redis 默认一致）。
func (e *QuotaEnforcer) Allow(ctx context.Context, tenantID, resource, dimension string, n int) (ratelimit.Result, error) {
	limit := e.Limit(ctx, tenantID, resource)
	if limit == domain.QuotaUnlimited || limit <= 0 {
		return ratelimit.Result{Allowed: true}, nil
	}
	result := e.backend.AllowN(quotaKey(tenantID, resource, dimension), float64(limit), int(limit), n)
	if !result.Allowed {
		return result, apperr.TooManyRequests("quota exhausted for " + resource)
	}
	return result, nil
}

// Check 计数型静态校验：预计用量不得超过上限。超限 42900。
//
// 席位执行（T-13 ensureSeatAvailable）已内联同语义；新计数型资源
// 应统一走本方法，避免散落的比较逻辑。
func (e *QuotaEnforcer) Check(ctx context.Context, tenantID, resource string, projected int64) error {
	limit := e.Limit(ctx, tenantID, resource)
	if limit == domain.QuotaUnlimited || limit < 0 {
		return nil
	}
	if projected > limit {
		return apperr.TooManyRequests("quota exceeded for " + resource)
	}
	return nil
}

// quotaKey 生成配额分键：quota:{tenant}:{resource}[:{dimension}]。
func quotaKey(tenantID, resource, dimension string) string {
	parts := []string{"quota", cleanKeySegment(tenantID), cleanKeySegment(resource)}
	if trimmed := cleanKeySegment(dimension); trimmed != "" {
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, ":")
}

// cleanKeySegment 键段清洗：空白与冒号折叠为下划线，空段占位。
func cleanKeySegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	return strings.NewReplacer(" ", "_", ":", "_").Replace(s)
}
