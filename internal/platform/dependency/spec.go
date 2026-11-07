package dependency

import (
	"context"
	"errors"
	"time"

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
