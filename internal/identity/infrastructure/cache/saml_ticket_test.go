package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// TestMemorySAMLTicketStoreConsumesOnce 校验 ticket 单次消费与过期语义。
func TestMemorySAMLTicketStoreConsumesOnce(t *testing.T) {
	t.Parallel()
	clock := &tunableClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	store := NewMemorySAMLTicketStore(clock)
	ctx := context.Background()
	payload := app.SAMLTicket{
		AccountID: "acct-1",
		Provider:  "test",
		IPHash:    "ip",
		Device:    domain.DeviceInfo{},
		TraceID:   "trace-1",
	}
	if err := store.Put(ctx, "ticket-1", payload, time.Minute); err != nil {
		t.Fatalf("put ticket: %v", err)
	}
	got, err := store.Consume(ctx, "ticket-1")
	if err != nil {
		t.Fatalf("consume ticket: %v", err)
	}
	if got.AccountID != "acct-1" || got.TraceID != "trace-1" {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if _, err := store.Consume(ctx, "ticket-1"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("expected not found on replay, got %v", err)
	}

	if err := store.Put(ctx, "ticket-2", payload, time.Minute); err != nil {
		t.Fatalf("put ticket-2: %v", err)
	}
	clock.Advance(2 * time.Minute)
	if _, err := store.Consume(ctx, "ticket-2"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("expected expired ticket not found, got %v", err)
	}
}

// TestMemorySAMLTicketStoreValidatesTTL 校验非正 TTL 拒绝写入。
func TestMemorySAMLTicketStoreValidatesTTL(t *testing.T) {
	t.Parallel()
	store := NewMemorySAMLTicketStore(nil)
	if err := store.Put(context.Background(), "ticket-1", app.SAMLTicket{AccountID: "acct-1"}, 0); err == nil {
		t.Fatalf("expected ttl error")
	}
}

// TestRedisSAMLTicketStoreUnavailableFailsClosed 校验无 Redis 时失败关闭。
func TestRedisSAMLTicketStoreUnavailableFailsClosed(t *testing.T) {
	t.Parallel()
	store := NewRedisSAMLTicketStore(nil)
	if err := store.Put(context.Background(), "ticket-1", app.SAMLTicket{AccountID: "acct-1"}, time.Minute); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("expected unavailable on put, got %v", err)
	}
	if _, err := store.Consume(context.Background(), "ticket-1"); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("expected unavailable on consume, got %v", err)
	}
}
