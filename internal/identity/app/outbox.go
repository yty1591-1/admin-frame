package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// 身份域异步主题与事件类型（ENT-01 T9）。
const (
	// TopicAudit 认证审计事件主题。
	TopicAudit = "identity.audit"
	// EventTypeAudit 审计事件类型；载荷为 audit.Event。
	EventTypeAudit = "audit.event"
)

// OutboxEmitter 把审计事件写入通用 Outbox，由 Relay 异步投递。
//
// 与 SlogEmitter 的差异：Emit 只保证「事件已写入 async_outbox」，
// 因此必须与账号 / 会话写入处于同一事务（docs/identity-authn.md §11.2）：
// 业务回滚时审计一并回滚，避免「登录成功但审计丢失」。
// 写入失败仍然失败关闭：调用方必须让认证写入返回 5xx。
type OutboxEmitter struct {
	// Events 通用 Outbox Store；nil 时返回内部错误（禁止静默丢弃审计）。
	Events outbox.Store
}

// 编译期断言：OutboxEmitter 必须满足审计端口。
var _ audit.Emitter = OutboxEmitter{}

// Emit 校验事件并写入 Outbox（topic=identity.audit）。
//
// 复用审计事件 ID 作为 outbox 主键：同一事件不会重复入队。
func (e OutboxEmitter) Emit(ctx context.Context, event audit.Event) error {
	if e.Events == nil {
		return apperr.Internal("identity outbox store is not configured", nil)
	}
	if err := event.Validate(); err != nil {
		return apperr.Internal("invalid audit event", err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return apperr.Internal("encode audit event", err)
	}
	record, err := outbox.NewEvent(outbox.NewEventParams{
		Topic: TopicAudit,
		Envelope: mq.Envelope{
			EventID:    event.EventID,
			EventType:  EventTypeAudit,
			Version:    "v1",
			TenantID:   event.MerchantID,
			OccurredAt: event.OccurredAt,
			TraceID:    event.TraceID,
			Payload:    payload,
		},
		AggregateType: event.ResourceType,
		AggregateID:   event.ResourceID,
		Now:           event.OccurredAt,
	})
	if err != nil {
		return apperr.Internal("build outbox event", err)
	}
	if err := e.Events.Append(ctx, record); err != nil {
		return apperr.Internal("append identity outbox event", err)
	}
	return nil
}

// AuditEventHandler 返回身份域异步事件消费 handler（注册到 identity.audit）。
//
// 载荷不可解析 / 事件类型未知属于永久失败（毒消息），由 Relay 写入死信；
// 审计出口失败按可重试错误处理，由 Relay 指数退避重试。
func AuditEventHandler(emitter audit.Emitter) outbox.Handler {
	return func(ctx context.Context, event *outbox.Event) error {
		if event == nil {
			return retry.Permanent(fmt.Errorf("identity.audit: nil outbox event"))
		}
		switch event.EventType {
		case EventTypeAudit:
			var auditEvent audit.Event
			if err := json.Unmarshal(event.Payload, &auditEvent); err != nil {
				return retry.Permanent(fmt.Errorf("identity.audit: decode payload: %w", err))
			}
			if err := auditEvent.Validate(); err != nil {
				return retry.Permanent(fmt.Errorf("identity.audit: invalid payload: %w", err))
			}
			if emitter == nil {
				return apperr.Internal("audit emitter is not configured", nil)
			}
			if err := emitter.Emit(ctx, auditEvent); err != nil {
				return fmt.Errorf("emit audit event: %w", err)
			}
			return nil
		default:
			return retry.Permanent(fmt.Errorf("identity.audit: unsupported event type %q", event.EventType))
		}
	}
}
