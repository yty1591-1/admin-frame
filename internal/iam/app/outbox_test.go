package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
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

// listOutbox 返回 now 之后到期的事件（按 ID 升序），用于断言状态。
func listOutbox(t *testing.T, repo *persistence.MemoryOutboxRepository, now time.Time) []*domain.OutboxEvent {
	t.Helper()
	events, err := repo.ListDue(context.Background(), now.Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	return events
}

func TestOutboxRecorderWritesPendingAuditEvent(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	recorder := app.OutboxRecorder{Events: repo}

	event := testAuditEvent(t)
	if err := recorder.Record(ctx, event); err != nil {
		t.Fatalf("Record: %v", err)
	}
	due := listOutbox(t, repo, time.Now().UTC())
	if len(due) != 1 {
		t.Fatalf("outbox size = %d, want 1", len(due))
	}
	stored := due[0]
	if stored.ID != event.EventID || stored.Type != domain.OutboxEventAudit {
		t.Fatalf("stored = %+v", stored)
	}
	if stored.AggregateType != event.ResourceType || stored.AggregateID != event.ResourceID {
		t.Fatalf("aggregate = %s/%s", stored.AggregateType, stored.AggregateID)
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

func TestOutboxRecorderRequiresRepository(t *testing.T) {
	recorder := app.OutboxRecorder{}
	if err := recorder.Record(context.Background(), testAuditEvent(t)); err == nil {
		t.Fatal("nil repository must fail, not silently drop audit")
	}
}

func TestOutboxRecorderRejectsInvalidEvent(t *testing.T) {
	repo := persistence.NewMemoryOutboxRepository()
	event := testAuditEvent(t)
	event.TraceID = ""
	if err := (app.OutboxRecorder{Events: repo}).Record(context.Background(), event); err == nil {
		t.Fatal("invalid audit event must be rejected")
	}
	if len(listOutbox(t, repo, time.Now().UTC())) != 0 {
		t.Fatal("invalid event must not be enqueued")
	}
}

func TestOutboxPublisherDeliversAndMarksSucceeded(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	if err := (app.OutboxRecorder{Events: repo}).Record(ctx, testAuditEvent(t)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	emitter := &capturedEmitter{}
	observer := &outboxObserver{}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events:   repo,
		Deliver:  app.AuditOutboxDeliverer(emitter),
		Observer: observer,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOutboxPublisher: %v", err)
	}
	delivered, err := publisher.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if delivered != 1 || emitter.count() != 1 {
		t.Fatalf("delivered=%d emitted=%d", delivered, emitter.count())
	}
	if got := observer.results(); len(got) != 1 || got[0] != "success" {
		t.Fatalf("observer = %v", got)
	}
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 0 {
		t.Fatalf("pending after delivery = %d", stats.Pending)
	}
	// 已投递事件不会被再次拉取。
	if again, err := publisher.Drain(ctx); err != nil || again != 0 {
		t.Fatalf("second drain = %d, %v", again, err)
	}
}

func TestOutboxPublisherRetriesThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	if err := (app.OutboxRecorder{Events: repo}).Record(ctx, testAuditEvent(t)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	emitter := &capturedEmitter{err: errors.New("audit sink down")}
	observer := &outboxObserver{}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events:    repo,
		Deliver:   app.AuditOutboxDeliverer(emitter),
		Policy:    domain.OutboxRetryPolicy{Base: time.Second, Max: time.Minute, MaxAttempts: 2},
		BatchSize: 10,
		Observer:  observer,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOutboxPublisher: %v", err)
	}

	if _, err := publisher.Drain(ctx); err == nil {
		t.Fatal("first failure must be reported")
	}
	// 事件回到 pending，且退避未到期时不会被再次拉取。
	if due := listOutbox(t, repo, now); len(due) != 1 || due[0].Attempts != 1 || due[0].NextRetryAt == nil {
		t.Fatalf("after first failure: %+v", due)
	}

	now = now.Add(2 * time.Second)
	if _, err := publisher.Drain(ctx); err == nil {
		t.Fatal("second failure must be reported")
	}
	if due := listOutbox(t, repo, now); len(due) != 0 {
		t.Fatalf("dead letter must not be listed as due: %+v", due)
	}
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 1 {
		t.Fatalf("dead letter must stay in pending gauge: %+v", stats)
	}
	if got := observer.results(); len(got) != 2 || got[0] != "retry" || got[1] != "dead" {
		t.Fatalf("observer = %v", got)
	}
}

func TestOutboxPublisherReclaimsStaleProcessing(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	if err := (app.OutboxRecorder{Events: repo}).Record(ctx, testAuditEvent(t)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// 模拟上一个 worker 认领后崩溃：processing 且 updated_at 早于租约。
	old := now.Add(-3 * domain.OutboxProcessingLease)
	if _, err := repo.Claim(ctx, testAuditEvent(t).EventID, old); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	emitter := &capturedEmitter{}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events:  repo,
		Deliver: app.AuditOutboxDeliverer(emitter),
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOutboxPublisher: %v", err)
	}
	delivered, err := publisher.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if delivered != 1 || emitter.count() != 1 {
		t.Fatalf("stale processing event must be redelivered: delivered=%d emitted=%d", delivered, emitter.count())
	}
}

func TestOutboxPublisherPoisonIsNotRetried(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	poison, err := domain.NewOutboxEvent(domain.NewOutboxEventParams{
		ID:            "3001",
		Type:          domain.OutboxEventCacheInvalidate,
		AggregateType: "iam_binding",
		AggregateID:   "4001",
		Payload:       []byte(`{"subject":"1"}`),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}
	if err := repo.Append(ctx, poison); err != nil {
		t.Fatalf("Append: %v", err)
	}
	observer := &outboxObserver{}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events:   repo,
		Deliver:  app.AuditOutboxDeliverer(&capturedEmitter{}),
		Observer: observer,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOutboxPublisher: %v", err)
	}
	if _, err := publisher.Drain(ctx); err == nil {
		t.Fatal("poison event must surface an error")
	}
	if due := listOutbox(t, repo, now); len(due) != 0 {
		t.Fatalf("poison event must not be retried: %+v", due)
	}
	if got := observer.results(); len(got) != 1 || got[0] != "poison" {
		t.Fatalf("observer = %v", got)
	}
}

func TestOutboxPublisherCorruptPayloadIsPoison(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	event, err := domain.NewOutboxEvent(domain.NewOutboxEventParams{
		ID:            "3002",
		Type:          domain.OutboxEventAudit,
		AggregateType: "iam_role",
		AggregateID:   "4002",
		// 合法 JSON 对象但缺少审计必填字段 → 永久不可投递。
		Payload: []byte(`{"action":""}`),
		Now:     now,
	})
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}
	if err := repo.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events:  repo,
		Deliver: app.AuditOutboxDeliverer(&capturedEmitter{}),
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewOutboxPublisher: %v", err)
	}
	if _, err := publisher.Drain(ctx); !errors.Is(err, app.ErrOutboxPoison) {
		t.Fatalf("err=%v, want ErrOutboxPoison", err)
	}
}

func TestOutboxPublisherRunStopsOnContextCancel(t *testing.T) {
	repo := persistence.NewMemoryOutboxRepository()
	emitter := &capturedEmitter{}
	publisher, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{
		Events:  repo,
		Deliver: app.AuditOutboxDeliverer(emitter),
	})
	if err != nil {
		t.Fatalf("NewOutboxPublisher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- publisher.Run(ctx, 10*time.Millisecond) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("publisher must stop when context is cancelled")
	}
}

func TestNewOutboxPublisherRequiresDependencies(t *testing.T) {
	if _, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{Deliver: func(context.Context, *domain.OutboxEvent) error { return nil }}); err == nil {
		t.Fatal("missing repository must error")
	}
	if _, err := app.NewOutboxPublisher(app.OutboxPublisherOptions{Events: persistence.NewMemoryOutboxRepository()}); err == nil {
		t.Fatal("missing deliver function must error")
	}
}

// outboxObserver 记录投递结果。
type outboxObserver struct {
	mu      sync.Mutex
	records []string
}

func (o *outboxObserver) OnOutboxDelivered(_ context.Context, result string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, result)
}

func (o *outboxObserver) results() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.records...)
}
