package iam_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// newSQLOutboxEvent 构造 outbox 测试事件；id 使用单调递增的雪花样式数字串。
func newSQLOutboxEvent(t *testing.T, id string, now time.Time) *domain.OutboxEvent {
	t.Helper()
	event, err := domain.NewOutboxEvent(domain.NewOutboxEventParams{
		ID:            id,
		Type:          domain.OutboxEventAudit,
		AggregateType: "iam_role",
		AggregateID:   "2001",
		Payload:       []byte(`{"event_id":"` + id + `","action":"rbac.role.change"}`),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}
	return event
}

// TestSQLOutboxLifecycle 覆盖 iam_outbox 的入队、认领、退避重试与终态统计。
func TestSQLOutboxLifecycle(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo := persistence.NewSQLOutboxRepository(sqlDB)
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)

	if err := repo.Append(ctx, newSQLOutboxEvent(t, "9001", now)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := repo.Append(ctx, newSQLOutboxEvent(t, "9001", now)); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate append err=%v, want 40900", err)
	}

	due, err := repo.ListDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 || due[0].ID != "9001" || string(due[0].Status) != string(domain.OutboxPending) {
		t.Fatalf("ListDue = %+v", due)
	}
	if due[0].Type != domain.OutboxEventAudit || due[0].AggregateID != "2001" {
		t.Fatalf("scanned event = %+v", due[0])
	}

	claimed, err := repo.Claim(ctx, "9001", now)
	if err != nil || !claimed {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}
	if claimed, err = repo.Claim(ctx, "9001", now); err != nil || claimed {
		t.Fatalf("second claim = %v, %v (want false, nil)", claimed, err)
	}
	if due, err = repo.ListDue(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("processing event must not be due: %v %v", due, err)
	}

	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 1 || !stats.OldestCreatedAt.Equal(now) {
		t.Fatalf("stats while processing = %+v", stats)
	}

	// processing 状态下才允许终态迁移，防止并发写覆盖。
	next := now.Add(time.Second)
	if err := repo.Reschedule(ctx, "9001", 1, next, now); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	if due, err = repo.ListDue(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("backoff must hide event: %v %v", due, err)
	}
	due, err = repo.ListDue(ctx, next, 10)
	if err != nil || len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("due after backoff = %+v, %v", due, err)
	}
	if claimed, err = repo.Claim(ctx, "9001", next); err != nil || !claimed {
		t.Fatalf("reclaim = %v, %v", claimed, err)
	}
	if err := repo.MarkSucceeded(ctx, "9001", next); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, "9001", next); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("second MarkSucceeded err=%v, want 40900", err)
	}
	if stats, err = repo.Stats(ctx); err != nil || stats.Pending != 0 {
		t.Fatalf("stats after success = %+v, %v", stats, err)
	}
}

// TestSQLOutboxReclaimsStaleProcessing 验证租约过期的 in-flight 事件会被回收重投。
func TestSQLOutboxReclaimsStaleProcessing(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo := persistence.NewSQLOutboxRepository(sqlDB)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

	if err := repo.Append(ctx, newSQLOutboxEvent(t, "9201", now)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := repo.Claim(ctx, "9201", now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if n, err := repo.ReclaimStale(ctx, now.Add(-time.Minute), now); err != nil || n != 0 {
		t.Fatalf("reclaim before lease = %d, %v", n, err)
	}
	if n, err := repo.ReclaimStale(ctx, now.Add(time.Second), now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("reclaim after lease = %d, %v", n, err)
	}
	due, err := repo.ListDue(ctx, now.Add(time.Second), 10)
	if err != nil || len(due) != 1 || string(due[0].Status) != string(domain.OutboxPending) {
		t.Fatalf("reclaimed event must be due: %+v, %v", due, err)
	}
}

// TestSQLOutboxDeadAndPoisonStayVisible 验证死信 / 毒消息仍计入积压口径。
func TestSQLOutboxDeadAndPoisonStayVisible(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	repo := persistence.NewSQLOutboxRepository(sqlDB)
	now := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)

	if err := repo.Append(ctx, newSQLOutboxEvent(t, "9101", now)); err != nil {
		t.Fatalf("Append dead: %v", err)
	}
	if _, err := repo.Claim(ctx, "9101", now); err != nil {
		t.Fatalf("Claim dead: %v", err)
	}
	if err := repo.MarkDead(ctx, "9101", now); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}

	if err := repo.Append(ctx, newSQLOutboxEvent(t, "9102", now)); err != nil {
		t.Fatalf("Append poison: %v", err)
	}
	if _, err := repo.Claim(ctx, "9102", now); err != nil {
		t.Fatalf("Claim poison: %v", err)
	}
	if err := repo.MarkPoison(ctx, "9102", now); err != nil {
		t.Fatalf("MarkPoison: %v", err)
	}

	if due, err := repo.ListDue(ctx, now.Add(time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("terminal events must not be due: %v %v", due, err)
	}
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 2 || !stats.OldestCreatedAt.Equal(now) {
		t.Fatalf("terminal events must stay visible: %+v", stats)
	}
}
