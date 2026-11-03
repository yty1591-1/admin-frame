package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// captureEmitter 记录被投递的审计事件。
type captureEmitter struct {
	events []audit.Event
}

func (e *captureEmitter) Emit(_ context.Context, event audit.Event) error {
	e.events = append(e.events, event)
	return nil
}

// sampleEvent 构造一条最小合法审计事件。
func sampleEvent(id string) audit.Event {
	return audit.Event{
		EventID:      id,
		Action:       "tenant.create",
		ActorID:      "u-1",
		ActorType:    "user",
		Channel:      "admin",
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   time.Now().UTC(),
		TraceID:      "trace-1",
		Service:      "admin-api",
	}
}

// TestOutboxEmitterAppendsEvent 验证事件写入指定 topic，载荷可被 handler 还原。
func TestOutboxEmitterAppendsEvent(t *testing.T) {
	store := outbox.NewMemoryStore()
	emitter := audit.OutboxEmitter{Topic: "tenant.audit", Events: store}
	event := sampleEvent("evt-1")
	if err := emitter.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}

	stored, ok := store.Event(event.EventID)
	if !ok {
		t.Fatalf("事件未写入 Outbox")
	}
	if stored.Topic != "tenant.audit" || stored.EventType != audit.EventTypeAudit {
		t.Fatalf("topic/type = %s/%s", stored.Topic, stored.EventType)
	}

	capture := &captureEmitter{}
	if err := audit.OutboxHandler(capture)(context.Background(), stored); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if len(capture.events) != 1 || capture.events[0].EventID != event.EventID {
		t.Fatalf("投递结果 = %+v", capture.events)
	}
}

// TestOutboxEmitterRequiresTopicAndStore 验证配置缺失时失败关闭，而不是静默丢弃。
func TestOutboxEmitterRequiresTopicAndStore(t *testing.T) {
	if err := (audit.OutboxEmitter{Events: outbox.NewMemoryStore()}).Emit(
		context.Background(), sampleEvent("evt-2")); err == nil {
		t.Fatal("缺少 topic 应返回错误")
	}
	if err := (audit.OutboxEmitter{Topic: "tenant.audit"}).Emit(
		context.Background(), sampleEvent("evt-3")); err == nil {
		t.Fatal("缺少 store 应返回错误")
	}
}
