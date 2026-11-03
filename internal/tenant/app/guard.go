package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// 守卫缓存默认值。
const (
	// DefaultStatusTTL 租户状态缓存时长。
	//
	// 必须显著小于「停用 ≤ 60s 生效」的验收上限：请求侧最坏延迟 = TTL + 一次查询，
	// 30s 给数据库与网络抖动留出余量；调大前先确认验收口径未变。
	DefaultStatusTTL = 30 * time.Second
	// DefaultNotFoundTTL 未登记租户的负缓存时长。
	//
	// 比正缓存短：新建租户后最多 5s 即可通过守卫，不至于把「刚开户」挡在门外，
	// 同时避免未登记 merchant_id 每请求打一次库。
	DefaultNotFoundTTL = 5 * time.Second
)

// 守卫判定错误。
var (
	// ErrTenantNotActive 租户存在但不在服务期（pending / suspended / terminated）。
	ErrTenantNotActive = errors.New("tenant: not in service")
	// ErrTenantUnknown 租户未登记（数据库里没有该 merchant_id）。
	ErrTenantUnknown = errors.New("tenant: unknown")
)

// GuardOptions 是守卫参数。
type GuardOptions struct {
	// TTL 正缓存时长；<=0 取 DefaultStatusTTL。
	TTL time.Duration
	// NotFoundTTL 负缓存时长；<=0 取 DefaultNotFoundTTL。
	NotFoundTTL time.Duration
	// Clock 时钟注入（测试用）；nil 取 time.Now。
	Clock func() time.Time
	// Observer 可观测端口（T-16）；nil 时使用 noop 实现。
	Observer Observer
}

// guardEntry 是单个商户的状态缓存项。
type guardEntry struct {
	status  domain.Status
	unknown bool
	expires time.Time
}

// Guard 判定某个商户当前是否可服务（ENT-09 停用生效链路）。
//
// 设计取舍：
//   - **fail closed**：仓储报错时返回错误而不是放行，由调用方映射为 503。
//     租户状态查不出来时无法证明「还在服务期」，放行等于停用失效。
//   - **不跨请求持锁**：冷启动时同一商户可能被并发查多次，这是可接受的代价；
//     用全局锁串行化会让所有商户的状态查询排队，故障时放大延迟。
//   - **错误不缓存**：数据库抖动不能变成 30s 的「已停用」误判。
type Guard struct {
	repo        domain.Repository
	observer    Observer
	ttl         time.Duration
	notFoundTTL time.Duration
	now         func() time.Time

	mu      sync.Mutex
	entries map[string]guardEntry
}

// NewGuard 构造守卫；repo 必填。
func NewGuard(repo domain.Repository, opts GuardOptions) (*Guard, error) {
	if repo == nil {
		return nil, apperr.Internal("tenant guard requires a repository", nil)
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultStatusTTL
	}
	notFoundTTL := opts.NotFoundTTL
	if notFoundTTL <= 0 {
		notFoundTTL = DefaultNotFoundTTL
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Guard{
		repo:        repo,
		observer:    observerOrNoop(opts.Observer),
		ttl:         ttl,
		notFoundTTL: notFoundTTL,
		now:         clock,
		entries:     make(map[string]guardEntry),
	}, nil
}

// Check 判定商户是否可服务；merchantID 为空表示平台级请求，直接放行。
//
// 返回 nil 表示放行；ErrTenantNotActive / ErrTenantUnknown 表示应拒绝（403）；
// 其它错误表示判定失败（503），调用方不得降级为放行。
func (g *Guard) Check(ctx context.Context, merchantID string) error {
	trimmed := strings.TrimSpace(merchantID)
	if trimmed == "" {
		return nil
	}
	if entry, ok := g.lookup(trimmed); ok {
		g.observer.GuardCacheHit(true)
		g.observe(entry)
		return entry.toError(trimmed)
	}
	g.observer.GuardCacheHit(false)

	tenant, err := g.repo.Get(ctx, trimmed)
	switch {
	case err == nil:
		entry := guardEntry{status: tenant.Status, expires: g.now().Add(g.ttl)}
		g.store(trimmed, entry)
		g.observe(entry)
		return serviceabilityError(trimmed, tenant.Status)
	case errors.Is(err, domain.ErrNotFound):
		entry := guardEntry{unknown: true, expires: g.now().Add(g.notFoundTTL)}
		g.store(trimmed, entry)
		g.observer.GuardDecision("deny")
		return fmt.Errorf("%w: %s", ErrTenantUnknown, trimmed)
	default:
		// 不缓存：数据库故障不应被记成「已停用」。
		g.observer.GuardDecision("error")
		return fmt.Errorf("tenant: guard lookup %s: %w", trimmed, err)
	}
}

// observe 按缓存项的服务性记录判定结果（deny / allow）。
func (g *Guard) observe(entry guardEntry) {
	if entry.unknown || !entry.status.Serving() {
		g.observer.GuardDecision("deny")
		return
	}
	g.observer.GuardDecision("allow")
}

// Verify 实现 platform.TenantGuard：把判定结果收敛为「放行 / 拒绝 / 无法判定」。
//
// 与 Check 的区别只在错误形态：Check 保留具体原因供调用方区分，Verify 按中间件
// 契约把「明确拒绝」与「判定失败」分开——后者必须 503 而不是 403，
// 否则数据库抖动会被误报成租户停用，值班会去查根本不存在的停用记录。
func (g *Guard) Verify(ctx context.Context, merchantID string) (bool, error) {
	err := g.Check(ctx, merchantID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrTenantNotActive), errors.Is(err, ErrTenantUnknown):
		return false, nil
	default:
		return false, err
	}
}

// Invalidate 主动清除某个商户的缓存。
//
// 仅供测试与「同进程内确有状态变更」的场景使用。A 端与 B 端是两个进程、
// 各自持有独立缓存，A 端的迁移无法触达 B 端实例，因此生产收敛完全依赖 TTL
// （验收上限 60s）；不要指望在管理端调用本方法能让业务端立即停用。
func (g *Guard) Invalidate(tenantID string) {
	trimmed := strings.TrimSpace(tenantID)
	if trimmed == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, trimmed)
}

// InvalidateAll 清空全部缓存（测试与运维手工干预用）。
func (g *Guard) InvalidateAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entries = make(map[string]guardEntry)
}

// lookup 读取未过期缓存。
func (g *Guard) lookup(merchantID string) (guardEntry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.entries[merchantID]
	if !ok {
		return guardEntry{}, false
	}
	if !g.now().Before(entry.expires) {
		delete(g.entries, merchantID)
		return guardEntry{}, false
	}
	return entry, true
}

// store 写入缓存项。
func (g *Guard) store(merchantID string, entry guardEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entries[merchantID] = entry
}

// toError 把缓存项还原为判定结果。
func (e guardEntry) toError(merchantID string) error {
	if e.unknown {
		return fmt.Errorf("%w: %s", ErrTenantUnknown, merchantID)
	}
	return serviceabilityError(merchantID, e.status)
}

// serviceabilityError 按状态给出判定结果：只有 active 放行。
func serviceabilityError(merchantID string, status domain.Status) error {
	if status.Serving() {
		return nil
	}
	return fmt.Errorf("%w: %s (status=%s)", ErrTenantNotActive, merchantID, status)
}
