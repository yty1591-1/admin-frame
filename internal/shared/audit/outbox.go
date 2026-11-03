package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// EventTypeAudit 是审计事件的 Outbox 事件类型；载荷为序列化后的 Event。
const EventTypeAudit = "audit.event"

// OutboxEmitter 把审计事件写入通用 Outbox，由 Relay 异步投递到下游出口。
//
// 与直接写审计表的差异：Emit 只保证「事件已进入业务事务」，
// 因此调用方必须让业务写入与本次 Emit 处于同一事务（pkg/db.WithinUnitOfWork）；
// 业务回滚时审计一并回滚，业务提交则审计必然可投递。
//
// 失败语义是失败关闭：Topic 未配置、事件非法、Outbox 写入失败都返回错误，
// 调用方不得吞掉——「审计可以延迟，不可以丢失」。
type OutboxEmitter struct {
	// Topic 业务域主题，如 tenant.audit。
	Topic string
	// Events 通用 Outbox Store；nil 时返回错误（禁止静默丢弃审计）。
	Events outbox.Store
}

// 编译期断言：OutboxEmitter 必须满足审计端口。
var _ Emitter = OutboxEmitter{}

// Emit 校验事件并写入 Outbox；复用审计事件 ID 作为 Outbox 主键，重复写入幂等。
func (e OutboxEmitter) Emit(ctx context.Context, event Event) error {
	topic := strings.TrimSpace(e.Topic)
	if topic == "" {
		return apperr.Internal("audit outbox topic is not configured", nil)
	}
	if e.Events == nil {
		return apperr.Internal("audit outbox store is not configured", nil)
	}
	if err := event.Validate(); err != nil {
		return apperr.Internal("invalid audit event", err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return apperr.Internal("encode audit event", err)
	}
	record, err := outbox.NewEvent(outbox.NewEventParams{
		Topic: topic,
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
		return apperr.Internal("build audit outbox event", err)
	}
	if err := e.Events.Append(ctx, record); err != nil {
		return apperr.Internal("append audit outbox event", err)
	}
	return nil
}

// OutboxHandler 返回审计 topic 的消费 handler：把 Outbox 事件投递到给定出口。
//
// 载荷不可解析 / 事件类型未知属于永久失败（毒消息），由 Relay 写入死信；
// 下游出口失败按可重试错误处理，由 Relay 指数退避重试（event_id 幂等）。
func OutboxHandler(emitter Emitter) outbox.Handler {
	return func(ctx context.Context, event *outbox.Event) error {
		if event == nil {
			return retry.Permanent(fmt.Errorf("audit outbox: nil event"))
		}
		if event.EventType != EventTypeAudit {
			return retry.Permanent(fmt.Errorf("audit outbox: unsupported event type %q", event.EventType))
		}
		var auditEvent Event
		if err := json.Unmarshal(event.Payload, &auditEvent); err != nil {
			return retry.Permanent(fmt.Errorf("audit outbox: decode payload: %w", err))
		}
		if err := auditEvent.Validate(); err != nil {
			return retry.Permanent(fmt.Errorf("audit outbox: invalid payload: %w", err))
		}
		if emitter == nil {
			return apperr.Internal("audit emitter is not configured", nil)
		}
		if err := emitter.Emit(ctx, auditEvent); err != nil {
			return fmt.Errorf("emit audit event: %w", err)
		}
		return nil
	}
}
