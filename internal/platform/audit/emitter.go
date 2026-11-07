package audit

import (
	"context"
	"log/slog"
	"time"

	sharedaudit "github.com/admin-frame/admin-frame/internal/shared/audit"
)

// SlogEmitter 将审计事件写入结构化日志（脚手架默认实现，AUDIT_STANDARDS §9）。
//
// 内存模式与本地开发使用本实现；生产必须叠加 SQLEmitter 或 OutboxEmitter
// 形成持久化出口，单个 Slog 出口不构成可审计留存。
type SlogEmitter struct {
	Log *slog.Logger
}

// 编译期断言：SlogEmitter 必须满足审计端口。
var _ sharedaudit.Emitter = SlogEmitter{}

// Emit 校验后以 Info 级别写出，并标记 audit=true。
func (s SlogEmitter) Emit(ctx context.Context, e sharedaudit.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	attrs := []any{
		"audit", true,
		"event_id", e.EventID,
		"action", e.Action,
		"actor_id", e.ActorID,
		"channel", e.Channel,
		"resource_type", e.ResourceType,
		"resource_id", e.ResourceID,
		"outcome", string(e.Outcome),
		"occurred_at", e.OccurredAt.UTC().Format(time.RFC3339),
		"trace_id", e.TraceID,
		"service", e.Service,
	}
	if e.ActorType != "" {
		attrs = append(attrs, "actor_type", e.ActorType)
	}
	if e.MerchantID != "" {
		attrs = append(attrs, "merchant_id", e.MerchantID)
	}
	if e.Reason != "" {
		attrs = append(attrs, "reason", e.Reason)
	}
	for k, v := range e.Attrs {
		attrs = append(attrs, "attr_"+k, v)
	}
	log.InfoContext(ctx, "audit_event", attrs...)
	return nil
}
