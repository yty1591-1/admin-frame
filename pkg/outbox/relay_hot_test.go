package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// TestRelayHotParamsApply 验证运行期参数覆盖装配期常量（ADR-0015 `async.outbox.*`）。
func TestRelayHotParamsApply(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	for _, id := range []string{"evt-1", "evt-2", "evt-3"} {
		if err := store.Append(ctx, newEvent(t, id)); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	var delivered int
	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(context.Context, *outbox.Event) error {
			delivered++
			return nil
		},
		BatchSize: 1, // 装配期只允许每轮 1 条
		Params: func() outbox.Params {
			return outbox.Params{BatchSize: 3}
		},
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}

	handled, err := relay.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	if handled != 3 || delivered != 3 {
		t.Fatalf("handled = %d, delivered = %d, want 3 (hot batch_size must win)", handled, delivered)
	}
}

// TestRelayHotMaxAttemptsShortensRetry 验证热更的 max_attempts 立即改变死信判定。
func TestRelayHotMaxAttemptsShortensRetry(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	if err := store.Append(ctx, newEvent(t, "evt-1")); err != nil {
		t.Fatalf("append: %v", err)
	}

	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(context.Context, *outbox.Event) error {
			return errors.New("always fails")
		},
		// 装配期允许 10 次；热更收敛为 1 次，首次失败即进死信。
		Policy: outbox.RetryPolicy{Base: time.Millisecond, MaxAttempts: 10},
		Params: func() outbox.Params { return outbox.Params{MaxAttempts: 1} },
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if _, err := relay.DrainOnce(ctx); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	event, ok := store.Event("evt-1")
	if !ok || event.Status != outbox.StatusDead {
		t.Fatalf("event status = %v, want dead after hot max_attempts=1", event)
	}
}

// TestRelayParamsOutOfRangeClamped 验证越界的热更值按装配期规则收敛，
// 不会产生 0 条认领或无限重试。
func TestRelayParamsOutOfRangeClamped(t *testing.T) {
	ctx := context.Background()
	store := outbox.NewMemoryStore()
	if err := store.Append(ctx, newEvent(t, "evt-1")); err != nil {
		t.Fatalf("append: %v", err)
	}

	relay, err := outbox.NewRelay(outbox.RelayOptions{
		Store: store,
		Handler: func(context.Context, *outbox.Event) error {
			return nil
		},
		Params: func() outbox.Params {
			// 超出 [1,500] 与 [1,20] 的声明区间。
			return outbox.Params{BatchSize: 99999, MaxAttempts: 9999}
		},
	})
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	handled, err := relay.DrainOnce(ctx)
	if err != nil || handled != 1 {
		t.Fatalf("handled = %d, err = %v", handled, err)
	}
}

// TestIdempotentHotVisibility 验证消费租约窗口可按次热更，且非法值回落。
func TestIdempotentHotVisibility(t *testing.T) {
	ctx := context.Background()
	deduper := &visibilityRecorder{}
	hot := 30 * time.Second
	wrapped, err := outbox.Idempotent(
		func(context.Context, *outbox.Event) error { return nil },
		outbox.IdempotentOptions{
			Deduper:           deduper,
			Consumer:          "demo",
			VisibilityTimeout: time.Minute,
			Visibility:        func() time.Duration { return hot },
		})
	if err != nil {
		t.Fatalf("Idempotent: %v", err)
	}

	if err := wrapped(ctx, newEvent(t, "evt-1")); err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	hot = 90 * time.Second
	if err := wrapped(ctx, newEvent(t, "evt-2")); err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	hot = 0 // 坏发布：回落装配期 1m
	if err := wrapped(ctx, newEvent(t, "evt-3")); err != nil {
		t.Fatalf("wrapped: %v", err)
	}

	want := []time.Duration{30 * time.Second, 90 * time.Second, time.Minute}
	if len(deduper.windows) != len(want) {
		t.Fatalf("acquire calls = %d, want %d", len(deduper.windows), len(want))
	}
	for i, window := range want {
		if deduper.windows[i] != window {
			t.Fatalf("acquire %d window = %v, want %v", i, deduper.windows[i], window)
		}
	}
}

// visibilityRecorder 记录每次抢占使用的接管窗口。
type visibilityRecorder struct {
	windows []time.Duration
}

func (r *visibilityRecorder) Acquire(_ context.Context, _, _ string, _ time.Time,
	visibility time.Duration) (outbox.DedupeOutcome, error) {
	r.windows = append(r.windows, visibility)
	return outbox.DedupeAcquired, nil
}

func (r *visibilityRecorder) Complete(_ context.Context, _, _ string) error { return nil }

func (r *visibilityRecorder) Release(_ context.Context, _, _ string) error { return nil }
