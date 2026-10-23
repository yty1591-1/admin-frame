package outbox_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/outbox"
)

func TestIdempotentSkipsCompletedEvent(t *testing.T) {
	deduper := outbox.NewMemoryDeduper()
	var calls int32
	handler, err := outbox.Idempotent(func(context.Context, *outbox.Event) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}, outbox.IdempotentOptions{Deduper: deduper, Consumer: "demo-consumer"})
	if err != nil {
		t.Fatalf("Idempotent: %v", err)
	}

	event := newEvent(t, "evt-idempotent")
	if err := handler(context.Background(), event); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := handler(context.Background(), event); err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
	if status, ok := deduper.Status(event.EventID, "demo-consumer"); !ok || status != "done" {
		t.Fatalf("consumer record = %q, %v", status, ok)
	}
}

func TestIdempotentReleasesOnFailure(t *testing.T) {
	deduper := outbox.NewMemoryDeduper()
	var calls int32
	handler, err := outbox.Idempotent(func(context.Context, *outbox.Event) error {
		if atomic.AddInt32(&calls, 1) == 1 {
			return errors.New("temporary failure")
		}
		return nil
	}, outbox.IdempotentOptions{Deduper: deduper, Consumer: "demo-consumer"})
	if err != nil {
		t.Fatalf("Idempotent: %v", err)
	}

	event := newEvent(t, "evt-retry")
	if err := handler(context.Background(), event); err == nil {
		t.Fatal("expected first delivery error")
	}
	if _, ok := deduper.Status(event.EventID, "demo-consumer"); ok {
		t.Fatal("failed delivery must release the consumer record")
	}
	if err := handler(context.Background(), event); err != nil {
		t.Fatalf("retry delivery: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestIdempotentBusyWithinVisibility(t *testing.T) {
	deduper := outbox.NewMemoryDeduper()
	now := time.Now().UTC()
	if outcome, err := deduper.Acquire(context.Background(), "evt-busy", "demo-consumer", now, time.Minute); err != nil ||
		outcome != outbox.DedupeAcquired {
		t.Fatalf("seed acquire = %v, %v", outcome, err)
	}

	var calls int32
	handler, err := outbox.Idempotent(func(context.Context, *outbox.Event) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}, outbox.IdempotentOptions{
		Deduper:           deduper,
		Consumer:          "demo-consumer",
		VisibilityTimeout: time.Minute,
		Now:               func() time.Time { return now.Add(time.Second) },
	})
	if err != nil {
		t.Fatalf("Idempotent: %v", err)
	}

	err = handler(context.Background(), newEvent(t, "evt-busy"))
	if !errors.Is(err, outbox.ErrConsumerBusy) {
		t.Fatalf("expected ErrConsumerBusy, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("handler calls = %d, want 0", got)
	}
}

func TestMemoryDeduperTakeOverAfterVisibility(t *testing.T) {
	deduper := outbox.NewMemoryDeduper()
	now := time.Now().UTC()
	if outcome, err := deduper.Acquire(context.Background(), "evt-takeover", "demo-consumer", now, time.Minute); err != nil ||
		outcome != outbox.DedupeAcquired {
		t.Fatalf("seed acquire = %v, %v", outcome, err)
	}
	if outcome, err := deduper.Acquire(context.Background(), "evt-takeover", "demo-consumer", now.Add(30*time.Second), time.Minute); err != nil ||
		outcome != outbox.DedupeBusy {
		t.Fatalf("acquire within visibility = %v, %v", outcome, err)
	}
	if outcome, err := deduper.Acquire(context.Background(), "evt-takeover", "demo-consumer", now.Add(2*time.Minute), time.Minute); err != nil ||
		outcome != outbox.DedupeAcquired {
		t.Fatalf("acquire after visibility = %v, %v", outcome, err)
	}
}

func TestIdempotentValidation(t *testing.T) {
	handler := func(context.Context, *outbox.Event) error { return nil }
	if _, err := outbox.Idempotent(nil, outbox.IdempotentOptions{Deduper: outbox.NewMemoryDeduper(), Consumer: "c"}); err == nil {
		t.Fatal("expected error for nil handler")
	}
	if _, err := outbox.Idempotent(handler, outbox.IdempotentOptions{Consumer: "c"}); err == nil {
		t.Fatal("expected error for nil deduper")
	}
	if _, err := outbox.Idempotent(handler, outbox.IdempotentOptions{Deduper: outbox.NewMemoryDeduper()}); err == nil {
		t.Fatal("expected error for empty consumer")
	}
}
