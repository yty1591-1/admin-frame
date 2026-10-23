package outbox_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

func newEvent(t *testing.T, id string) *outbox.Event {
	t.Helper()
	event, err := outbox.NewEvent(outbox.NewEventParams{
		Topic: "demo.created",
		Envelope: mq.Envelope{
			EventID:    id,
			EventType:  "demo.created",
			Version:    "v1",
			OccurredAt: time.Now(),
			Payload:    []byte(`{"id":"1"}`),
		},
	})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	return event
}

func TestNewEventValidatesEnvelope(t *testing.T) {
	if _, err := outbox.NewEvent(outbox.NewEventParams{
		Envelope: mq.Envelope{EventType: "demo.created", Version: "v1", Payload: []byte(`{}`)},
	}); err == nil {
		t.Fatal("expected error for missing event id")
	}
}

func TestRetryPolicy(t *testing.T) {
	policy := outbox.RetryPolicy{Base: time.Second, Max: 5 * time.Second, MaxAttempts: 4}
	if got := policy.NextRetry(1); got != time.Second {
		t.Fatalf("attempt 1 = %v", got)
	}
	if got := policy.NextRetry(2); got != 2*time.Second {
		t.Fatalf("attempt 2 = %v", got)
	}
	if got := policy.NextRetry(3); got != 4*time.Second {
		t.Fatalf("attempt 3 = %v", got)
	}
	if got := policy.NextRetry(10); got != 5*time.Second {
		t.Fatalf("cap = %v", got)
	}
	if !policy.Exhausted(4) || policy.Exhausted(3) {
		t.Fatal("Exhausted boundary mismatch")
	}

	fixed := outbox.RetryPolicy{Base: time.Second, Backoff: outbox.BackoffFixed}
	if got := fixed.NextRetry(5); got != time.Second {
		t.Fatalf("fixed = %v", got)
	}
}

func TestMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	event := newEvent(t, "evt-1")
	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Append(ctx, event); err == nil {
		t.Fatal("expected duplicate append error")
	}

	now := time.Now().UTC()
	claimed, err := store.ClaimBatch(ctx, now, time.Minute, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimBatch = %d, %v", len(claimed), err)
	}
	if claimed[0].Status != outbox.StatusProcessing {
		t.Fatalf("status = %s", claimed[0].Status)
	}
	if again, _ := store.ClaimBatch(ctx, now, time.Minute, 10); len(again) != 0 {
		t.Fatalf("claimed twice: %d", len(again))
	}

	if err := store.Reschedule(ctx, "evt-1", 1, now.Add(time.Second), now, "boom"); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	if claimed, _ = store.ClaimBatch(ctx, now, time.Minute, 10); len(claimed) != 0 {
		t.Fatal("event claimed before next_retry_at")
	}
	claimed, _ = store.ClaimBatch(ctx, now.Add(2*time.Second), time.Minute, 10)
	if len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("reclaim = %+v", claimed)
	}

	if err := store.MarkDead(ctx, "evt-1", now, "exhausted"); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
	letters := store.DeadLetters()
	if len(letters) != 1 || letters[0].SourceID != "evt-1" {
		t.Fatalf("dead letters = %+v", letters)
	}
	stats, err := store.Stats(ctx)
	if err != nil || stats.Pending != 1 || stats.DLQ != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
}

func TestMemoryStoreReclaimStale(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	_ = store.Append(ctx, newEvent(t, "evt-1"))
	now := time.Now().UTC()
	if _, err := store.ClaimBatch(ctx, now, time.Minute, 10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	reclaimed, err := store.ReclaimStale(ctx, now.Add(-time.Second), now.Add(2*time.Minute))
	if err != nil || reclaimed != 0 {
		t.Fatalf("reclaimed = %d, %v", reclaimed, err)
	}
	reclaimed, err = store.ReclaimStale(ctx, now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil || reclaimed != 1 {
		t.Fatalf("reclaimed = %d, %v", reclaimed, err)
	}
}

func TestRelayDelivers(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	_ = store.Append(ctx, newEvent(t, "evt-1"))

	var delivered atomic.Int32
	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(context.Context, *outbox.Event) error {
			delivered.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	handled, err := relay.DrainOnce(ctx)
	if err != nil || handled != 1 || delivered.Load() != 1 {
		t.Fatalf("handled = %d, delivered = %d, err = %v", handled, delivered.Load(), err)
	}
	event, ok := store.Event("evt-1")
	if !ok || event.Status != outbox.StatusSucceeded {
		t.Fatalf("event = %+v", event)
	}
}

func TestRelayRetriesThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	_ = store.Append(ctx, newEvent(t, "evt-1"))

	now := time.Now().UTC()
	var calls atomic.Int32
	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(context.Context, *outbox.Event) error {
			calls.Add(1)
			return errors.New("temporary")
		},
		Policy: outbox.RetryPolicy{Base: time.Millisecond, MaxAttempts: 2},
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	event, _ := store.Event("evt-1")
	if event.Status != outbox.StatusPending || event.Attempts != 1 {
		t.Fatalf("after retry = %+v", event)
	}
	now = now.Add(time.Second)
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	event, _ = store.Event("evt-1")
	if event.Status != outbox.StatusDead || calls.Load() != 2 {
		t.Fatalf("after exhaust = %+v, calls = %d", event, calls.Load())
	}
	if letters := store.DeadLetters(); len(letters) != 1 {
		t.Fatalf("dead letters = %+v", letters)
	}
}

func TestRelayPoisonGoesToDLQ(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	_ = store.Append(ctx, newEvent(t, "evt-1"))

	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(context.Context, *outbox.Event) error {
			return retry.Permanent(errors.New("poison"))
		},
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	event, _ := store.Event("evt-1")
	if event.Status != outbox.StatusFailed {
		t.Fatalf("status = %s", event.Status)
	}
	if letters := store.DeadLetters(); len(letters) != 1 {
		t.Fatalf("dead letters = %+v", letters)
	}
}

func TestRelayObserverReportsResults(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	_ = store.Append(ctx, newEvent(t, "evt-ok"))
	_ = store.Append(ctx, newEvent(t, "evt-bad"))

	var (
		mu      sync.Mutex
		results = make(map[string]int)
	)
	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(_ context.Context, event *outbox.Event) error {
			if event.EventID == "evt-bad" {
				return retry.Permanent(errors.New("poison"))
			}
			return nil
		},
		Observer: outbox.ObserverFunc(func(_ context.Context, _ string, result string) {
			mu.Lock()
			results[result]++
			mu.Unlock()
		}),
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if results["success"] != 1 || results["poison"] != 1 {
		t.Fatalf("results = %+v", results)
	}
}

func TestRelayGracefulCancel(t *testing.T) {
	store := outbox.NewMemoryStore()
	_ = store.Append(context.Background(), newEvent(t, "evt-1"))

	started := make(chan struct{})
	release := make(chan struct{})
	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(context.Context, *outbox.Event) error {
			close(started)
			<-release
			return nil
		},
		ShutdownTimeout: 500 * time.Millisecond,
		PollInterval:    time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = relay.Run(ctx)
	}()
	<-started
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not shut down")
	}
}

func TestMemoryStoreReplayDeadLetter(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	_ = store.Append(ctx, newEvent(t, "evt-dead"))
	now := time.Now().UTC()
	if _, err := store.ClaimBatch(ctx, now, time.Minute, 10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if err := store.MarkFailed(ctx, "evt-dead", now, "poison"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if ok, err := store.ReplayDeadLetter(ctx, "evt-missing", now); err != nil || ok {
		t.Fatalf("replay missing = %v, %v", ok, err)
	}

	replayed, err := store.ReplayAllDeadLetters(ctx, now.Add(time.Second))
	if err != nil || replayed != 1 {
		t.Fatalf("ReplayAllDeadLetters = %d, %v", replayed, err)
	}
	event, _ := store.Event("evt-dead")
	if event.Status != outbox.StatusPending || event.Attempts != 0 || event.LastError != "" || event.NextRetryAt != nil {
		t.Fatalf("replayed event = %+v", event)
	}
	if letters := store.DeadLetters(); len(letters) != 1 || letters[0].ReplayedAt == nil {
		t.Fatalf("dead letters = %+v", letters)
	}
	if ok, err := store.ReplayDeadLetter(ctx, "evt-dead", now); err != nil || ok {
		t.Fatalf("double replay = %v, %v", ok, err)
	}
}
