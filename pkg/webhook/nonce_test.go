package webhook_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/webhook"
)

func TestMemoryNonceStoreClaim(t *testing.T) {
	store := webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{Clock: newMutableClock(signingTime)})
	ctx := t.Context()

	first, err := store.Claim(ctx, "msg_1", 10*time.Minute)
	if err != nil || !first {
		t.Fatalf("first claim=(%v,%v)", first, err)
	}
	second, err := store.Claim(ctx, "msg_1", 10*time.Minute)
	if err != nil || second {
		t.Fatalf("second claim=(%v,%v)", second, err)
	}
	other, err := store.Claim(ctx, "msg_2", 10*time.Minute)
	if err != nil || !other {
		t.Fatalf("other claim=(%v,%v)", other, err)
	}
	if got := store.Len(); got != 2 {
		t.Fatalf("len=%d want 2", got)
	}
}

func TestMemoryNonceStoreTTL(t *testing.T) {
	clk := newMutableClock(signingTime)
	store := webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{Clock: clk})
	ctx := t.Context()

	if first, err := store.Claim(ctx, "msg_ttl", 10*time.Minute); err != nil || !first {
		t.Fatalf("claim=(%v,%v)", first, err)
	}
	clk.Advance(10*time.Minute - time.Second)
	if first, err := store.Claim(ctx, "msg_ttl", 10*time.Minute); err != nil || first {
		t.Fatalf("claim before expiry=(%v,%v)", first, err)
	}
	clk.Advance(2 * time.Second)
	if first, err := store.Claim(ctx, "msg_ttl", 10*time.Minute); err != nil || !first {
		t.Fatalf("claim after expiry=(%v,%v)", first, err)
	}
}

func TestMemoryNonceStoreValidation(t *testing.T) {
	store := webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{})
	ctx := t.Context()

	if _, err := store.Claim(ctx, "", time.Minute); err == nil {
		t.Fatal("empty nonce should fail")
	}
	if _, err := store.Claim(ctx, "msg", 0); err == nil {
		t.Fatal("zero ttl should fail")
	}
	if _, err := store.Claim(ctx, strings.Repeat("a", 513), time.Minute); err == nil {
		t.Fatal("oversized nonce should fail")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Claim(canceled, "msg", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err=%v", err)
	}

	var nilStore *webhook.MemoryNonceStore
	if _, err := nilStore.Claim(ctx, "msg", time.Minute); err == nil {
		t.Fatal("nil store should fail")
	}
	var zeroStore webhook.MemoryNonceStore
	if _, err := zeroStore.Claim(ctx, "msg", time.Minute); err == nil {
		t.Fatal("zero store should fail")
	}
}

func TestMemoryNonceStoreCapacity(t *testing.T) {
	clk := newMutableClock(signingTime)
	store := webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{
		MaxEntries: 4,
		Shards:     1,
		Clock:      clk,
	})
	ctx := t.Context()
	for i := range 4 {
		if first, err := store.Claim(ctx, fmt.Sprintf("msg_%d", i), 10*time.Minute); err != nil || !first {
			t.Fatalf("claim %d=(%v,%v)", i, first, err)
		}
	}
	_, err := store.Claim(ctx, "msg_overflow", 10*time.Minute)
	if !errors.Is(err, webhook.ErrNonceStoreFull) {
		t.Fatalf("err=%v want ErrNonceStoreFull", err)
	}

	clk.Advance(11 * time.Minute)
	if first, err := store.Claim(ctx, "msg_after_sweep", 10*time.Minute); err != nil || !first {
		t.Fatalf("claim after sweep=(%v,%v)", first, err)
	}
}

func TestMemoryNonceStoreConcurrent(t *testing.T) {
	store := webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{})
	ctx := t.Context()
	const workers = 128

	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			first, err := store.Claim(ctx, "msg_race", time.Minute)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if first {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("winners=%d want 1", got)
	}
	if got := store.Len(); got != 1 {
		t.Fatalf("len=%d want 1", got)
	}
}

func TestMemoryNonceStoreReset(t *testing.T) {
	store := webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{})
	ctx := t.Context()
	if first, err := store.Claim(ctx, "msg_reset", time.Minute); err != nil || !first {
		t.Fatalf("claim=(%v,%v)", first, err)
	}
	store.Reset()
	if got := store.Len(); got != 0 {
		t.Fatalf("len=%d want 0", got)
	}
	if first, err := store.Claim(ctx, "msg_reset", time.Minute); err != nil || !first {
		t.Fatalf("claim after reset=(%v,%v)", first, err)
	}
}
