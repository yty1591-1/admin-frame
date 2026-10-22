package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func newOutboxEvent(t *testing.T, id string, now time.Time) *domain.OutboxEvent {
	t.Helper()
	event, err := domain.NewOutboxEvent(domain.NewOutboxEventParams{
		ID:            id,
		Type:          domain.OutboxEventAudit,
		AggregateType: "iam_role",
		AggregateID:   "2001",
		Payload:       []byte(`{"action":"rbac.role.change"}`),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}
	return event
}

func TestMemoryOutboxLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)

	if err := repo.Append(ctx, newOutboxEvent(t, "1", now)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := repo.Append(ctx, newOutboxEvent(t, "1", now)); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate append err=%v, want 40900", err)
	}

	due, err := repo.ListDue(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 || due[0].ID != "1" {
		t.Fatalf("ListDue = %+v", due)
	}

	claimed, err := repo.Claim(ctx, "1", now)
	if err != nil || !claimed {
		t.Fatalf("Claim = %v, %v", claimed, err)
	}
	if claimed, err = repo.Claim(ctx, "1", now); err != nil || claimed {
		t.Fatalf("second claim = %v, %v (want false, nil)", claimed, err)
	}
	if due, err = repo.ListDue(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("processing event must not be due: %v %v", due, err)
	}
	if err := repo.MarkSucceeded(ctx, "1", now); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	if err := repo.MarkSucceeded(ctx, "1", now); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("second MarkSucceeded err=%v, want 40900", err)
	}
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 0 || !stats.OldestCreatedAt.IsZero() {
		t.Fatalf("stats after success = %+v", stats)
	}
}

func TestMemoryOutboxRetryVisibility(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	if err := repo.Append(ctx, newOutboxEvent(t, "2", now)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := repo.Claim(ctx, "2", now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	next := now.Add(time.Second)
	if err := repo.Reschedule(ctx, "2", 1, next, now); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	if due, err := repo.ListDue(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatalf("event must wait for backoff: %v %v", due, err)
	}
	due, err := repo.ListDue(ctx, next, 10)
	if err != nil {
		t.Fatalf("ListDue after backoff: %v", err)
	}
	if len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("due after backoff = %+v", due)
	}

	if _, err := repo.Claim(ctx, "2", next); err != nil {
		t.Fatalf("Claim again: %v", err)
	}
	if err := repo.MarkDead(ctx, "2", next); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 1 || !stats.OldestCreatedAt.Equal(now) {
		t.Fatalf("dead letter must stay visible in stats: %+v", stats)
	}
}

func TestMemoryOutboxPoisonAndLimit(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	for _, id := range []string{"1", "2", "3"} {
		if err := repo.Append(ctx, newOutboxEvent(t, id, now)); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}
	due, err := repo.ListDue(ctx, now, 2)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 2 || due[0].ID != "1" || due[1].ID != "2" {
		t.Fatalf("ListDue limit/order = %+v", due)
	}
	if _, err := repo.Claim(ctx, "1", now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := repo.MarkPoison(ctx, "1", now); err != nil {
		t.Fatalf("MarkPoison: %v", err)
	}
	if due, err = repo.ListDue(ctx, now, 10); err != nil || len(due) != 2 || due[0].ID != "2" {
		t.Fatalf("poison event must not be redelivered: %v %v", due, err)
	}
	stats, err := repo.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 3 {
		t.Fatalf("stats = %+v, want 3 (poison + 2 pending)", stats)
	}
}

func TestMemoryOutboxReclaimsStaleProcessing(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	if err := repo.Append(ctx, newOutboxEvent(t, "3", now)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := repo.Claim(ctx, "3", now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// 租约未过期：不回收。
	if n, err := repo.ReclaimStale(ctx, now.Add(-time.Minute), now); err != nil || n != 0 {
		t.Fatalf("reclaim before lease = %d, %v", n, err)
	}
	// 租约过期：回收为 pending 并可再次投递。
	if n, err := repo.ReclaimStale(ctx, now.Add(time.Second), now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("reclaim after lease = %d, %v", n, err)
	}
	due, err := repo.ListDue(ctx, now.Add(time.Second), 10)
	if err != nil || len(due) != 1 || due[0].Status != domain.OutboxPending {
		t.Fatalf("reclaimed event must be due: %v %v", due, err)
	}
}

func TestMemoryOutboxClonesState(t *testing.T) {
	ctx := context.Background()
	repo := persistence.NewMemoryOutboxRepository()
	now := time.Now().UTC()
	source := newOutboxEvent(t, "1", now)
	if err := repo.Append(ctx, source); err != nil {
		t.Fatalf("Append: %v", err)
	}
	source.Payload[0] = '['
	source.Status = domain.OutboxSucceeded
	due, err := repo.ListDue(ctx, now.Add(time.Hour), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("ListDue: %v %v", due, err)
	}
	if due[0].Payload[0] == '[' || due[0].Status != domain.OutboxPending {
		t.Fatalf("stored event mutated through caller pointer: %+v", due[0])
	}
}
