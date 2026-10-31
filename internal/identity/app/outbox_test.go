package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// captureAudit 记录 handler 投递的审计事件。
type captureAudit struct {
	events []audit.Event
	err    error
}

func (c *captureAudit) Emit(_ context.Context, event audit.Event) error {
	if c.err != nil {
		return c.err
	}
	c.events = append(c.events, event)
	return nil
}

// validAuditEvent 构造可通过校验的最小审计事件。
func validAuditEvent() audit.Event {
	return audit.Event{
		EventID:      "evt-1",
		Action:       audit.ActionAuthLogin,
		ActorID:      "acct-1",
		ActorType:    "staff",
		Channel:      "admin",
		MerchantID:   "m-1",
		ResourceType: "account",
		ResourceID:   "acct-1",
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   time.Now().UTC(),
		TraceID:      "trace-1",
		Service:      "identity",
	}
}

// TestOutboxEmitterAndHandler 验证审计事件写入 Outbox 并可被消费者还原（T9）。
func TestOutboxEmitterAndHandler(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	emitter := app.OutboxEmitter{Events: store}
	event := validAuditEvent()
	if err := emitter.Emit(ctx, event); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	records, err := store.ClaimBatch(ctx, event.OccurredAt, 0, 1)
	if err != nil || len(records) != 1 {
		t.Fatalf("ClaimBatch = %d, err=%v", len(records), err)
	}
	capture := &captureAudit{}
	handler := app.AuditEventHandler(capture)
	if err := handler(ctx, records[0]); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if len(capture.events) != 1 || capture.events[0].EventID != event.EventID {
		t.Fatalf("captured = %+v", capture.events)
	}
}

// TestOutboxEmitterRejectsMissingStore 验证未配置 Store 时失败关闭。
func TestOutboxEmitterRejectsMissingStore(t *testing.T) {
	if err := (app.OutboxEmitter{}).Emit(context.Background(), validAuditEvent()); err == nil {
		t.Fatal("nil store must reject audit emit")
	}
	store := outbox.NewMemoryStore()
	invalid := validAuditEvent()
	invalid.Action = ""
	if err := (app.OutboxEmitter{Events: store}).Emit(context.Background(), invalid); err == nil {
		t.Fatal("invalid audit event must be rejected")
	}
}

// failingStore 模拟 Outbox 写入失败；其余方法委托内存存储。
type failingStore struct{ outbox.Store }

func (failingStore) Append(context.Context, *outbox.Event) error {
	return context.DeadlineExceeded
}

// TestOutboxEmitterFailsClosedOnStoreError 验证写入失败必须向上返回 5xx。
func TestOutboxEmitterFailsClosedOnStoreError(t *testing.T) {
	err := (app.OutboxEmitter{Events: failingStore{Store: outbox.NewMemoryStore()}}).Emit(context.Background(), validAuditEvent())
	if err == nil {
		t.Fatal("store failure must fail closed")
	}
}

// TestAuditHandlerRejectsPoisonMessages 验证毒消息进入永久失败。
func TestAuditHandlerRejectsPoisonMessages(t *testing.T) {
	ctx := context.Background()
	handler := app.AuditEventHandler(&captureAudit{})
	if err := handler(ctx, nil); err == nil {
		t.Fatal("nil event must be permanent failure")
	}
	if err := handler(ctx, &outbox.Event{Envelope: mq.Envelope{
		EventType: app.EventTypeAudit, Payload: []byte("{"),
	}}); err == nil {
		t.Fatal("invalid payload must be permanent failure")
	}
	if err := handler(ctx, &outbox.Event{Envelope: mq.Envelope{EventType: "unknown"}}); err == nil {
		t.Fatal("unknown event type must be permanent failure")
	}
	payload, _ := json.Marshal(validAuditEvent())
	if err := handler(ctx, &outbox.Event{Envelope: mq.Envelope{
		EventType: app.EventTypeAudit, Payload: payload,
	}}); err != nil {
		t.Fatalf("valid event error = %v", err)
	}
}
