package ratelimit_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

func TestKeyedIsolatesKeys(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	k, err := ratelimit.NewKeyed(func() (ratelimit.Limiter, error) {
		return ratelimit.NewTokenBucket(1, 1, ratelimit.WithClock(clock))
	}, ratelimit.WithKeyedClock(clock))
	if err != nil {
		t.Fatal(err)
	}

	if !k.Allow("a").Allowed {
		t.Fatal("a first")
	}
	if k.Allow("a").Allowed {
		t.Fatal("a second should deny")
	}
	if !k.Allow("b").Allowed {
		t.Fatal("b should be independent")
	}
	if k.Len() != 2 {
		t.Fatalf("len=%d", k.Len())
	}
}

func TestKeyedIdleTTL(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	k := ratelimit.MustKeyed(func() (ratelimit.Limiter, error) {
		return ratelimit.NewTokenBucket(100, 1, ratelimit.WithClock(clock))
	}, ratelimit.WithKeyedClock(clock), ratelimit.WithIdleTTL(time.Second))

	_ = k.Allow("x")
	if k.Len() != 1 {
		t.Fatal("expected 1")
	}
	clock.Advance(2 * time.Second)
	k.Sweep()
	if k.Len() != 0 {
		t.Fatalf("len=%d after sweep", k.Len())
	}
}

func TestKeyedNilFactory(t *testing.T) {
	if _, err := ratelimit.NewKeyed(nil); err == nil {
		t.Fatal("expected error")
	}
}
