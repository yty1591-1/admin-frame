package ratelimit_test

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

func TestSlidingWindowLimit(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	w, err := ratelimit.NewSlidingWindow(3, time.Second, ratelimit.WithSlidingClock(clock))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if r := w.Allow(); !r.Allowed {
			t.Fatalf("allow %d failed: %+v", i, r)
		}
	}
	if r := w.Allow(); r.Allowed {
		t.Fatal("4th should deny")
	}

	// 整窗边界处上一窗权重仍为 1.0；再推进半窗使加权降到约 1.5，可继续放行。
	clock.Advance(time.Second + 500*time.Millisecond)
	if r := w.Allow(); !r.Allowed {
		t.Fatalf("after partial next window should allow: %+v", r)
	}
}

func TestSlidingWindowWeightedOverlap(t *testing.T) {
	clock := ratelimit.NewManualClock(time.Unix(0, 0).UTC())
	w := ratelimit.MustSlidingWindow(10, time.Second, ratelimit.WithSlidingClock(clock))

	for i := 0; i < 10; i++ {
		if !w.Allow().Allowed {
			t.Fatalf("fill %d", i)
		}
	}
	// 进入下一窗一半：上一窗权重约 0.5 → 加权约 5，仍可再要 5
	clock.Advance(1500 * time.Millisecond)
	allowed := 0
	for w.Allow().Allowed {
		allowed++
		if allowed > 10 {
			t.Fatal("too many allows")
		}
	}
	if allowed < 4 || allowed > 6 {
		t.Fatalf("expected ~5 allows under half previous weight, got %d", allowed)
	}
}

func TestSlidingWindowInvalid(t *testing.T) {
	if _, err := ratelimit.NewSlidingWindow(0, time.Second); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ratelimit.NewSlidingWindow(1, 0); err == nil {
		t.Fatal("expected error")
	}
}
