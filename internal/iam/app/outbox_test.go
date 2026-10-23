package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

// capturedEmitter 记录投递到审计出口的事件，可注入失败。
type capturedEmitter struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (e *capturedEmitter) Emit(_ context.Context, event audit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.events = append(e.events, event)
	return nil
}

func (e *capturedEmitter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

func testAuditEvent(t *testing.T) audit.Event {
	t.Helper()
	event := audit.Event{
		EventID:      "1001",
		Action:       audit.ActionRBACRoleChange,
		ActorID:      "staff-1",
		ActorType:    "staff",
		Channel:      "admin",
		ResourceType: app.ResourceTypeRole,
		ResourceID:   "2001",
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		TraceID:      "trace-1",
		Service:      "admin-api",
		Attrs:        map[string]string{app.AttrOperation: "create"},
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("invalid test event: %v", err)
	}
	return event
}

func TestOutboxRecorderWritesPendingAuditEvent(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	recorder := app.OutboxRecorder{Events: store}

	event := testAuditEvent(t)
	if err := recorder.Record(ctx, event); err != nil {
		t.Fatalf("Record: %v", err)
	}
	stored, ok := store.Event(event.EventID)
	if !ok {
		t.Fatal("outbox must contain the audit event")
	}
	if stored.Topic != app.TopicAudit || stored.EventType != app.EventTypeAudit {
		t.Fatalf("routing = %s/%s", stored.Topic, stored.EventType)
	}
	if stored.AggregateType != event.ResourceType || stored.AggregateID != event.ResourceID {
		t.Fatalf("aggregate = %s/%s", stored.AggregateType, stored.AggregateID)
	}
	if stored.Status != outbox.StatusPending {
		t.Fatalf("status = %s, want pending", stored.Status)
	}
	var decoded audit.Event
	if err := json.Unmarshal(stored.Payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded.Action != event.Action || decoded.ActorID != event.ActorID {
		t.Fatalf("decoded = %+v", decoded)
	}

	// 同一事件重复写入必须失败关闭，而不是静默去重。
	if err := recorder.Record(ctx, event); err == nil {
		t.Fatal("duplicate audit event must fail the write (fail-closed)")
	}
}

func TestOutboxRecorderRequiresStore(t *testing.T) {
	recorder := app.OutboxRecorder{}
	if err := recorder.Record(context.Background(), testAuditEvent(t)); err == nil {
		t.Fatal("nil store must fail, not silently drop audit")
	}
}

func TestOutboxRecorderRejectsInvalidEvent(t *testing.T) {
	store := outbox.NewMemoryStore()
	event := testAuditEvent(t)
	event.TraceID = ""
	if err := (app.OutboxRecorder{Events: store}).Record(context.Background(), event); err == nil {
		t.Fatal("invalid audit event must be rejected")
	}
	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 0 {
		t.Fatal("invalid event must not be enqueued")
	}
}

func TestAuditEventHandlerDeliversAuditEvent(t *testing.T) {
	event := testAuditEvent(t)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	emitter := &capturedEmitter{}
	handler := app.AuditEventHandler(emitter)

	if err := handler(context.Background(), &outbox.Event{
		Envelope: envelope(event.EventID, app.EventTypeAudit, payload),
		Topic:    app.TopicAudit,
	}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if emitter.count() != 1 {
		t.Fatalf("emitted = %d, want 1", emitter.count())
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	if emitter.events[0].EventID != event.EventID {
		t.Fatalf("emitted event = %+v", emitter.events[0])
	}
}

func TestAuditEventHandlerRetriesEmitterFailure(t *testing.T) {
	event := testAuditEvent(t)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	handler := app.AuditEventHandler(&capturedEmitter{err: errors.New("audit sink down")})

	err = handler(context.Background(), &outbox.Event{
		Envelope: envelope(event.EventID, app.EventTypeAudit, payload),
		Topic:    app.TopicAudit,
	})
	if err == nil {
		t.Fatal("emitter failure must surface as retryable error")
	}
	if retry.IsPermanent(err) {
		t.Fatalf("emitter failure must stay retryable, got permanent: %v", err)
	}
}

func TestAuditEventHandlerPoisonsCorruptPayload(t *testing.T) {
	handler := app.AuditEventHandler(&capturedEmitter{})
	err := handler(context.Background(), &outbox.Event{
		Envelope: envelope("2001", app.EventTypeAudit, []byte(`{`)),
		Topic:    app.TopicAudit,
	})
	if err == nil || !retry.IsPermanent(err) {
		t.Fatalf("corrupt payload must be a permanent failure, got %v", err)
	}
}

func TestAuditEventHandlerPoisonsUnknownEventType(t *testing.T) {
	handler := app.AuditEventHandler(&capturedEmitter{})
	err := handler(context.Background(), &outbox.Event{
		Envelope: envelope("3001", "iam.unknown", []byte(`{}`)),
		Topic:    app.TopicAudit,
	})
	if err == nil || !retry.IsPermanent(err) {
		t.Fatalf("unknown event type must be a permanent failure, got %v", err)
	}
}

func TestAuditEventHandlerIgnoresLegacyCacheInvalidation(t *testing.T) {
	handler := app.AuditEventHandler(&capturedEmitter{})
	if err := handler(context.Background(), &outbox.Event{
		Envelope: envelope("4001", app.EventTypeCacheInvalidate, []byte(`{"subject":"1"}`)),
		Topic:    app.TopicCache,
	}); err != nil {
		t.Fatalf("legacy cache invalidation must be a no-op, got %v", err)
	}
}

func TestAuditTopicObserverFiltersOtherTopics(t *testing.T) {
	recorder := &deliveryRecorder{}
	observer := app.AuditTopicObserver{Metrics: recorder}
	observer.OnOutboxDelivered(context.Background(), app.TopicAudit, "success")
	observer.OnOutboxDelivered(context.Background(), "order.created", "success")
	if len(recorder.results) != 1 || recorder.results[0] != "success" {
		t.Fatalf("results = %v, want only iam.audit", recorder.results)
	}
}

type deliveryRecorder struct {
	results []string
}

func (r *deliveryRecorder) OnOutboxDelivered(_ context.Context, result string) {
	r.results = append(r.results, result)
}

func envelope(eventID, eventType string, payload []byte) mq.Envelope {
	return mq.Envelope{
		EventID:    eventID,
		EventType:  eventType,
		Version:    "v1",
		OccurredAt: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		TraceID:    "trace-1",
		Payload:    payload,
	}
}
