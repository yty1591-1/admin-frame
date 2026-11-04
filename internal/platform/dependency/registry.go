// Package dependency 是外部依赖的统一注册表（ENT-13）：
// 按依赖名登记超时 / 重试 / 熔断参数，产出带指标的执行包装。
//
// 目标（关闭判据）：下游故障快速失败（熔断 + 超时）；依赖指标可见
// （dependency_call_total / dependency_retry_total）。
//
// 使用约束：
//   - 每个出站依赖在装配期登记一次（Spec 名全局唯一，低基数——指标标签）；
//   - 调用方经 Caller.Do 执行，禁止绕过注册表直连出站客户端
//     （check-deps.sh 门禁守护新增引用）；
//   - 重试只用于幂等安全的方法（GET / 状态推进类），非幂等调用
//     显式传 NoRetry——注册表不猜语义。
package dependency

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// Caller 是登记后的依赖执行句柄。
type Caller interface {
	// Do 执行一次依赖调用：单次尝试 = 超时 context 包 breaker 包 fn；
	// 登记了重试策略且 fn 声明可重试时按退避重试。
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// BreakerSpec 熔断参数（字段语义对齐 pkg/circuitbreaker.Config）。
type BreakerSpec struct {
	// ConsecutiveFailures 连续失败 N 次后打开；≤0 默认 5。
	ConsecutiveFailures int
	// OpenTimeout 打开态冷却时长；≤0 默认 30s。
	OpenTimeout time.Duration
	// IsSuccessful 判定调用是否成功（业务拒绝类错误不应计入失败）；
	// nil 视为 err == nil 即成功。
	IsSuccessful func(err error) bool
}

// Spec 是依赖登记参数。
type Spec struct {
	// Name 依赖名（如 oauth:github / vault / channel:smtp）；全局唯一、
	// 低基数——直接作为指标标签。
	Name string
	// Timeout 单次调用超时；≤0 默认 5s（REL 超时分层：注册表兜底，
	// 调用方仍应在 ctx 上带更短业务 deadline）。
	Timeout time.Duration
	// Retry 重试策略；零值 = 不重试（MaxAttempts<=1）。
	Retry retry.Policy
	// Breaker 熔断参数；nil = 不启用熔断（如 fail-fast 密钥读取）。
	Breaker *BreakerSpec
}

// Observer 是注册表的可观测端口（prometheus 适配在 observability 子包
// 或由 platform 装配注入）。
type Observer interface {
	// CallObserved 记录一次依赖调用（result: ok / error / breaker_open）。
	CallObserved(name, result string)
	// RetryObserved 记录一次重试触发。
	RetryObserved(name string)
}

type noopObserver struct{}

// CallObserved noop：不做任何记录。
func (noopObserver) CallObserved(string, string) {}

// RetryObserved noop：不做任何记录。
func (noopObserver) RetryObserved(string) {}

// ErrUnknownDependency 表示按名取用未登记的依赖（装配缺陷，快速失败）。
var ErrUnknownDependency = errors.New("dependency: not registered")

// Registry 是依赖注册表（进程级单例，装配期填充、运行期只读）。
type Registry struct {
	mu       sync.RWMutex
	callers  map[string]*caller
	observer Observer
}

// caller 是登记产物的内部实现。
type caller struct {
	spec    Spec
	breaker *circuitbreaker.Breaker
	obs     Observer
}

// NewRegistry 构造空注册表。
func NewRegistry(obs Observer) *Registry {
	if obs == nil {
		obs = noopObserver{}
	}
	return &Registry{callers: make(map[string]*caller), observer: obs}
}

// Register 登记依赖；重名 / 参数非法返回错误（装配期即失败）。
func (r *Registry) Register(spec Spec) (Caller, error) {
	if r == nil {
		return nil, errors.New("dependency: nil registry")
	}
	if name := trimName(spec.Name); name == "" {
		return nil, errors.New("dependency: spec name is required")
	}
	spec.Name = trimName(spec.Name)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.callers[spec.Name]; exists {
		return nil, fmt.Errorf("dependency: %q already registered", spec.Name)
	}
	c := &caller{spec: spec, obs: r.observer}
	if spec.Breaker != nil {
		breaker, err := circuitbreaker.New(circuitbreaker.Config{
			Name:    spec.Name,
			Timeout: orDefault(spec.Breaker.OpenTimeout, 30*time.Second),
			ReadyToTrip: func(counts circuitbreaker.Counts) bool {
				threshold := int64(spec.Breaker.ConsecutiveFailures)
				if threshold <= 0 {
					threshold = 5
				}
				return int64(counts.ConsecutiveFailures) >= threshold
			},
			IsSuccessful: spec.Breaker.IsSuccessful,
		})
		if err != nil {
			return nil, fmt.Errorf("dependency: build breaker for %q: %w", spec.Name, err)
		}
		c.breaker = breaker
	}
	r.callers[spec.Name] = c
	return c, nil
}

// Get 按名取用已登记依赖；未登记返回 ErrUnknownDependency（调用方
// 不应吞掉——装配缺陷在首次调用即暴露）。
func (r *Registry) Get(name string) (Caller, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.callers[trimName(name)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDependency, name)
	}
	return c, nil
}

// Names 返回已登记依赖名（诊断 / 指标对账）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.callers))
	for name := range r.callers {
		out = append(out, name)
	}
	return out
}

// Do 实现 Caller：超时 → 熔断 → 重试。
//
// 语义细节：
//   - 单次尝试 = 超时 context 包 breaker.Do 包 fn（快速失败双保险）；
//   - fn 返回 retry.Permanent 包装错误时终止重试（非幂等 / 永久错误）；
//   - 熔断打开的快速失败计 breaker_open（区别于普通 error）。
func (c *caller) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if fn == nil {
		return errors.New("dependency: nil fn")
	}
	policy := c.spec.Retry
	if policy.MaxAttempts <= 1 {
		return c.attempt(ctx, fn)
	}
	lastErr := retry.Do(ctx, policy, func(attemptCtx context.Context) error {
		err := c.attempt(attemptCtx, fn)
		if err != nil {
			c.obs.RetryObserved(c.spec.Name)
		}
		return err
	})
	return lastErr
}

// attempt 单次尝试：超时包熔断包 fn。
func (c *caller) attempt(ctx context.Context, fn func(ctx context.Context) error) error {
	timeout := orDefault(c.spec.Timeout, 5*time.Second)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	exec := func() error { return fn(callCtx) }
	if c.breaker != nil {
		inner := exec // 捕获旧值：闭包引用变量会自递归（Do 包 Do）。
		exec = func() error { return c.breaker.Do(inner) }
	}
	err := exec()
	switch {
	case err == nil:
		c.obs.CallObserved(c.spec.Name, "ok")
	case isBreakerOpen(err):
		c.obs.CallObserved(c.spec.Name, "breaker_open")
	default:
		c.obs.CallObserved(c.spec.Name, "error")
	}
	return err
}

// isBreakerOpen 判定熔断打开错误（pkg/circuitbreaker 哨兵）。
func isBreakerOpen(err error) bool {
	return errors.Is(err, circuitbreaker.ErrOpen)
}

func trimName(s string) string { return strings.TrimSpace(s) }

func orDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}
