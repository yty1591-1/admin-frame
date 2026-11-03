package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/audit/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/testkit"
)

// seedEvent 构造一条可落库的审计事件。
func seedEvent(id, action, actor string, occurred time.Time) audit.Event {
	return audit.Event{
		EventID:      id,
		Action:       action,
		ActorID:      actor,
		ActorType:    "admin",
		Channel:      "admin",
		ResourceType: "account",
		ResourceID:   "acct-1",
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   occurred,
		TraceID:      "trace-" + id,
		Service:      "admin-api",
	}
}

// TestAuditReaderAndRetention 覆盖审计表的查询、分页总量与到期清理（ENT-04 T4/T6）。
func TestAuditReaderAndRetention(t *testing.T) {
	ctx := context.Background()
	db := testkit.MySQL(t)
	sqlDB := db.SQL()
	if _, err := sqlDB.ExecContext(ctx, "DELETE FROM audit_event"); err != nil {
		t.Fatalf("clear audit table: %v", err)
	}

	now := time.Now().UTC()
	expired := now.AddDate(0, 0, -400)
	emitter := audit.SQLEmitter{DB: sqlDB}
	for _, event := range []audit.Event{
		seedEvent("evt-recent-1", audit.ActionAuthLogin, "admin-1", now.Add(-2*time.Hour)),
		seedEvent("evt-recent-2", audit.ActionAuthLogin, "admin-2", now.Add(-time.Hour)),
		seedEvent("evt-rbac", audit.ActionRBACRoleChange, "admin-1", now.Add(-30*time.Minute)),
		seedEvent("evt-expired", audit.ActionAuthLogin, "admin-9", expired),
	} {
		if err := emitter.Emit(ctx, event); err != nil {
			t.Fatalf("emit %s: %v", event.EventID, err)
		}
	}

	reader, err := persistence.NewSQLReader(sqlDB, "")
	if err != nil {
		t.Fatalf("NewSQLReader() error = %v", err)
	}

	filter := domain.Filter{
		From:  now.Add(-24 * time.Hour),
		To:    now.Add(time.Minute),
		Limit: 10,
	}
	events, err := reader.List(ctx, filter)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("List() returned %d events, want 3 within 24h", len(events))
	}
	if events[0].OccurredAt.Before(events[1].OccurredAt) {
		t.Fatalf("List() must be ordered by occurred_at DESC")
	}

	total, err := reader.Count(ctx, filter)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if total != 3 {
		t.Fatalf("Count() = %d, want 3", total)
	}

	byAction := filter
	byAction.Action = audit.ActionRBACRoleChange
	matched, err := reader.List(ctx, byAction)
	if err != nil {
		t.Fatalf("List(action) error = %v", err)
	}
	if len(matched) != 1 || matched[0].EventID != "evt-rbac" {
		t.Fatalf("List(action) = %+v, want only evt-rbac", matched)
	}

	byActor := filter
	byActor.ActorID = "admin-1"
	actorTotal, err := reader.Count(ctx, byActor)
	if err != nil {
		t.Fatalf("Count(actor) error = %v", err)
	}
	if actorTotal != 2 {
		t.Fatalf("Count(actor) = %d, want 2", actorTotal)
	}

	streamed := make([]string, 0, 3)
	if err := reader.Stream(ctx, filter, func(event audit.Event) error {
		streamed = append(streamed, event.EventID)
		return nil
	}); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(streamed) != 3 {
		t.Fatalf("Stream() yielded %d rows, want 3", len(streamed))
	}
	// ASC 排序：最后一条是最新的 evt-rbac（-30m），不是 evt-recent-2（-1h）。
	if streamed[0] != "evt-recent-1" || streamed[len(streamed)-1] != "evt-rbac" {
		t.Fatalf("Stream() must be ordered by occurred_at ASC, got %v", streamed)
	}

	store, err := persistence.NewSQLRetentionStore(sqlDB, "")
	if err != nil {
		t.Fatalf("NewSQLRetentionStore() error = %v", err)
	}
	cutoff := now.AddDate(0, 0, -180)
	purgeable, err := store.CountBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("CountBefore() error = %v", err)
	}
	if purgeable != 1 {
		t.Fatalf("CountBefore() = %d, want 1 (400 天前的事件)", purgeable)
	}
	deleted, err := store.DeleteBefore(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("DeleteBefore() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteBefore() = %d, want 1", deleted)
	}
	remaining, err := reader.Count(ctx, domain.Filter{
		From: expired.Add(-time.Hour), To: now.Add(time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatalf("Count() after purge error = %v", err)
	}
	if remaining != 3 {
		t.Fatalf("remaining rows = %d, want 3 (仅清理到期行)", remaining)
	}
}
