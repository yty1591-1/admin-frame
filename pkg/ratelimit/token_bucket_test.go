package ratelimit_test

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

func TestTokenBucketBurstThenRefill(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	b, err := ratelimit.NewTokenBucket(10, 2, ratelimit.WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}

	if r := b.Allow(); !r.Allowed {
		t.Fatal("1st should allow")
	}
	if r := b.Allow(); !r.Allowed {
		t.Fatal("2nd should allow (burst)")
	}
	r := b.Allow()
	if r.Allowed {
		t.Fatal("3rd should deny")
	}
	if r.RetryAfter <= 0 {
		t.Fatalf("RetryAfter=%s", r.RetryAfter)
	}
	if !stderrors.Is(r.Err(), ratelimit.ErrLimited) {
		t.Fatalf("err=%v", r.Err())
	}

	clock.Advance(200 * time.Millisecond) // +2 tokens at 10/s
	if r := b.Allow(); !r.Allowed {
		t.Fatal("after refill should allow")
	}
}

func TestTokenBucketAllowN(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	b := ratelimit.MustTokenBucket(1, 5, ratelimit.WithClock(clock))
	r := b.AllowN(5)
	if !r.Allowed || r.Remaining != 0 {
		t.Fatalf("r=%+v", r)
	}
	if b.AllowN(1).Allowed {
		t.Fatal("expected deny")
	}
	if b.AllowN(0).Allowed != true {
		t.Fatal("n<=0 should allow")
	}
}

func TestTokenBucketInvalid(t *testing.T) {
	if _, err := ratelimit.NewTokenBucket(0, 1); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ratelimit.NewTokenBucket(1, 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestTokenBucketWaitCancel(t *testing.T) {
	b := ratelimit.MustTokenBucket(0.001, 1) // very slow refill
	_ = b.Allow()                            // empty
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := b.Wait(ctx)
	if !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
