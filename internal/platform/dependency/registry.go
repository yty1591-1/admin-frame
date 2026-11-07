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
//     显式传 NoRetry——注册表不猜语义；
//   - HTTP 依赖经 Registry.HTTP 构造：请求级重试复用 pkg/httpclient 的
//     退避与 Retry-After 语义，超时 / 重试 / 熔断 / 指标口径与本表一致；
//   - 受 import 方向约束的下游驱动由装配层注入注册表客户端：SAML、OAuth
//     与 pkg/sms 已注入；pkg/push 尚未装配生产驱动，接入时按同一方式注入；
//     vault 在配置解析期执行（注册表尚未构造），仍直连 pkg/httpclient 并
//     接受 check-deps 守护。
package dependency

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Registry 是依赖注册表（进程级单例，装配期填充、运行期只读）。
type Registry struct {
	mu       sync.RWMutex
	callers  map[string]*caller
	observer Observer
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
	name, err := r.prepare(spec)
	if err != nil {
		return nil, err
	}
	spec.Name = name
	breaker, err := newBreaker(spec)
	if err != nil {
		return nil, err
	}
	c := &caller{spec: spec, breaker: breaker, obs: r.observer}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.callers[name]; exists {
		return nil, fmt.Errorf("dependency: %q already registered", name)
	}
	r.callers[name] = c
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

// prepare 校验注册表与依赖名并归一化。
func (r *Registry) prepare(spec Spec) (string, error) {
	if r == nil {
		return "", errors.New("dependency: nil registry")
	}
	name := trimName(spec.Name)
	if name == "" {
		return "", errors.New("dependency: spec name is required")
	}
	return name, nil
}

func trimName(s string) string { return strings.TrimSpace(s) }
