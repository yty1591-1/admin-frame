// Package observability 提供身份域撤销体系的 Prometheus 指标适配。
//
// 本包只把应用层观测接口翻译为指标，不参与撤销判定；由 cmd 在装配期创建一次，
// 同时注入 RevocationChecker 与 AuthService，保证读写两侧共享同一 Registry。
package observability

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/admin-frame/admin-frame/internal/identity/app"
)

// Metrics 是身份域撤销指标集，实现 app.RevocationObserver。
type Metrics struct {
	stateWriteErrors *prometheus.CounterVec
}

// 编译期断言：指标集必须满足应用层观测端口。
var _ app.RevocationObserver = (*Metrics)(nil)

// NewMetrics 创建并注册指标；重复注册视为装配错误（fail-fast）。
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	if registerer == nil {
		return nil, fmt.Errorf("identity observability: registerer is required")
	}
	m := &Metrics{
		stateWriteErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "identity_revocation_cache_write_error_total",
			Help: "身份域撤销快照写入失败次数；op=set 表示回填/刷新失败，op=delete 表示兜底删除失败（此时依赖 ≤60s TTL 兜底）。",
		}, []string{"op"}),
	}
	if err := registerer.Register(m.stateWriteErrors); err != nil {
		return nil, fmt.Errorf("identity observability: register metrics: %w", err)
	}
	// 预创建标签，保证无失败时指标仍可被采集，避免告警表达式因指标缺失而静默。
	for _, op := range []string{"set", "delete"} {
		m.stateWriteErrors.WithLabelValues(op)
	}
	return m, nil
}

// OnAccountStateWriteFailure 实现 app.RevocationObserver。
func (m *Metrics) OnAccountStateWriteFailure(_ context.Context, op string, _ error) {
	if m == nil {
		return
	}
	if op == "" {
		op = "unknown"
	}
	m.stateWriteErrors.WithLabelValues(op).Inc()
}
