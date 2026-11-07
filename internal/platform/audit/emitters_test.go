package audit_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/audit"
	sharedaudit "github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// failingEmitter 记录调用并返回固定错误，用于验证 MultiEmitter 失败即停。
type failingEmitter struct {
	calls int
	err   error
}

func (f *failingEmitter) Emit(_ context.Context, _ sharedaudit.Event) error {
	f.calls++
	return f.err
}

// captureEmitter 记录被投递的审计事件。
type captureEmitter struct {
	events []sharedaudit.Event
}

func (e *captureEmitter) Emit(_ context.Context, event sharedaudit.Event) error {
	e.events = append(e.events, event)
	return nil
}

// validEvent 构造一条最小合法审计事件（与 shared/audit 的校验基线一致）。
func validEvent() sharedaudit.Event {
	return sharedaudit.Event{
		EventID:      "evt-1",
		Action:       sharedaudit.ActionAuthLogin,
		ActorID:      "user-1",
		Channel:      "c",
		ResourceType: "session",
		ResourceID:   "sess-1",
		Outcome:      sharedaudit.OutcomeSuccess,
		OccurredAt:   time.Now().UTC(),
		TraceID:      "trace-1",
		Service:      "c-api",
	}
}

// TestSQLEmitterRequiresDatabase 验证未配置审计库时失败关闭，禁止静默丢弃。
func TestSQLEmitterRequiresDatabase(t *testing.T) {
	emitter := audit.SQLEmitter{}
	if err := emitter.Emit(context.Background(), validEvent()); err == nil {
		t.Fatal("emitter without database must fail closed")
	}
}

// TestSQLEmitterRejectsInvalidEvent 验证落库前执行审计事件校验。
func TestSQLEmitterRejectsInvalidEvent(t *testing.T) {
	event := validEvent()
	event.Service = ""
	emitter := audit.SQLEmitter{DB: nil}
	if err := emitter.Emit(context.Background(), event); err == nil {
		t.Fatal("invalid event must be rejected before database access")
	}
}

// TestMultiEmitterStopsOnFailure 验证多出口按顺序执行且任一口失败即停止。
func TestMultiEmitterStopsOnFailure(t *testing.T) {
	first := &failingEmitter{}
	second := &failingEmitter{}
	ctx := context.Background()
	if err := (audit.MultiEmitter{first, second}).Emit(ctx, validEvent()); err != nil {
		t.Fatal("nil failures should succeed")
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("calls = %d/%d, want 1/1", first.calls, second.calls)
	}

	first = &failingEmitter{err: context.Canceled}
	second = &failingEmitter{}
	if err := (audit.MultiEmitter{first, second}).Emit(ctx, validEvent()); err == nil {
		t.Fatal("first emitter failure must propagate")
	}
	if first.calls != 1 || second.calls != 0 {
		t.Fatalf("calls = %d/%d, want 1/0", first.calls, second.calls)
	}
}

// sampleEvent 构造一条最小合法审计事件。
func sampleEvent(id string) sharedaudit.Event {
	return sharedaudit.Event{
		EventID:      id,
		Action:       "tenant.create",
		ActorID:      "u-1",
		ActorType:    "user",
		Channel:      "admin",
		ResourceType: "tenant",
		ResourceID:   "t-1",
		Outcome:      sharedaudit.OutcomeSuccess,
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

// TestSlogEmitterEmit 验证结构化日志出口写出审计关键字段。
func TestSlogEmitterEmit(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	em := audit.SlogEmitter{Log: slog.New(h)}
	if err := em.Emit(context.Background(), validEvent()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, needle := range []string{`"audit":true`, `"action":"auth.login"`, `"event_id":"evt-1"`} {
		if !strings.Contains(out, needle) {
			t.Fatalf("log missing %s; got %s", needle, out)
		}
	}
}

// TestSlogEmitterRejectsInvalid 验证日志出口同样执行事件校验。
func TestSlogEmitterRejectsInvalid(t *testing.T) {
	em := audit.SlogEmitter{Log: slog.Default()}
	e := validEvent()
	e.Service = ""
	if err := em.Emit(context.Background(), e); err == nil {
		t.Fatal("expected validate error")
	}
}
