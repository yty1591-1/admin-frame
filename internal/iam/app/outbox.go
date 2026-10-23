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

// IAM 异步主题与事件类型（ENT-03 T8）。
const (
	// TopicAudit 审计事件主题。
	TopicAudit = "iam.audit"
	// TopicCache 缓存失效事件主题（历史事件，当前投递为无操作）。
	TopicCache = "iam.cache"
	// EventTypeAudit 审计事件类型；载荷为 audit.Event。
	EventTypeAudit = "audit.event"
	// EventTypeCacheInvalidate 缓存失效事件类型。
	EventTypeCacheInvalidate = "cache.invalidate"
)

// OutboxRecorder 把审计事件写入通用 Outbox，由 Relay 异步投递。
//
// 与 SlogRecorder 的差异：Record 只保证「事件已持久化到 async_outbox」，
// 投递失败不会回滚已提交的业务写入，而是通过重试 + 死信 + 积压指标暴露。
// 写入失败仍然失败关闭：调用方必须让业务响应返回 50000（AUD-06）。
type OutboxRecorder struct {
	// Events 通用 Outbox Store；nil 时返回内部错误（禁止静默丢弃审计）。
	Events outbox.Store
}

// Record 校验事件并写入 Outbox（topic=iam.audit）。
//
// 复用审计事件 ID 作为 outbox 主键：同一事件不会重复入队，
// 也让「审计事件」与「投递记录」可以在一次按 ID 查询中对上。
func (r OutboxRecorder) Record(ctx context.Context, event audit.Event) error {
	if r.Events == nil {
		return apperr.Internal("iam outbox store is not configured", nil)
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
	if err := r.Events.Append(ctx, record); err != nil {
		return apperr.Internal("append iam outbox event", err)
	}
	return nil
}

// AuditEventHandler 返回 IAM 异步事件消费 handler（注册到 iam.audit / iam.cache）。
//
// 载荷不可解析 / 事件类型未知属于永久失败（毒消息），由 Relay 写入死信；
// 审计出口失败按可重试错误处理，由 Relay 指数退避重试。
func AuditEventHandler(emitter audit.Emitter) outbox.Handler {
	return func(ctx context.Context, event *outbox.Event) error {
		if event == nil {
			return retry.Permanent(fmt.Errorf("iam.audit: nil outbox event"))
		}
		switch event.EventType {
		case EventTypeAudit:
			var auditEvent audit.Event
			if err := json.Unmarshal(event.Payload, &auditEvent); err != nil {
				return retry.Permanent(fmt.Errorf("iam.audit: decode payload: %w", err))
			}
			if err := auditEvent.Validate(); err != nil {
				return retry.Permanent(fmt.Errorf("iam.audit: invalid payload: %w", err))
			}
			if emitter == nil {
				return apperr.Internal("audit emitter is not configured", nil)
			}
			if err := emitter.Emit(ctx, auditEvent); err != nil {
				return fmt.Errorf("emit audit event: %w", err)
			}
			return nil
		case EventTypeCacheInvalidate:
			// 缓存失效由写入侧 invalidator + L1/L2 TTL 兜底；历史事件消费为无操作，
			// 避免迁移后残留事件反复进入死信。
			return nil
		default:
			return retry.Permanent(fmt.Errorf("iam.audit: unsupported event type %q", event.EventType))
		}
	}
}

// AuditTopicObserver 把通用 Relay 的投递结果映射到 IAM 指标（iam_outbox_delivery_total）。
type AuditTopicObserver struct {
	// Metrics IAM 指标出口，可为 nil。
	Metrics interface {
		OnOutboxDelivered(ctx context.Context, result string)
	}
}

// OnOutboxDelivered 实现 outbox.Observer；仅统计 IAM 审计主题。
func (o AuditTopicObserver) OnOutboxDelivered(ctx context.Context, topic, result string) {
	if o.Metrics == nil || topic != TopicAudit {
		return
	}
	o.Metrics.OnOutboxDelivered(ctx, result)
}

// 编译期断言：OutboxRecorder 必须满足用例侧审计端口。
var _ ChangeRecorder = OutboxRecorder{}
