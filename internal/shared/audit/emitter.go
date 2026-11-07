package audit

import (
	"context"
)

// Emitter 发出已校验的审计事件（AUD-03）。
//
// 本包只定义端口；具体出口（结构化日志、追加写审计表、事务内 Outbox）
// 实现在 internal/platform/audit，由 cmd 在装配期注入。这样任何只消费
// Event / Emitter 的域都不会被拖入 pkg/mq、pkg/outbox 等基础设施依赖。
type Emitter interface {
	Emit(ctx context.Context, e Event) error
}
