package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
)

func TestRealClock(t *testing.T) {
	before := time.Now()
	got := clock.RealClock().Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("now=%s outside [%s,%s]", got, before, after)
	}
}

func TestManual(t *testing.T) {
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m := clock.NewManual(start)
	if !m.Now().Equal(start) {
		t.Fatalf("now=%s", m.Now())
	}
	m.Advance(90 * time.Minute)
	if want := start.Add(90 * time.Minute); !m.Now().Equal(want) {
		t.Fatalf("now=%s want=%s", m.Now(), want)
	}
	m.Set(start)
	if !m.Now().Equal(start) {
		t.Fatalf("now=%s", m.Now())
	}
}

func TestManualZeroUsesEpoch(t *testing.T) {
	m := clock.NewManual(time.Time{})
	if !m.Now().Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("now=%s", m.Now())
	}
}

func TestFunc(t *testing.T) {
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := clock.Func(func() time.Time { return fixed })
	if !c.Now().Equal(fixed) {
		t.Fatalf("now=%s", c.Now())
	}

	var nilFn clock.Func
	if got := nilFn.Now(); !got.IsZero() {
		t.Fatalf("nil func now=%s", got)
	}
}

func TestManualNilReceiver(t *testing.T) {
	var m *clock.Manual
	if got := m.Now(); !got.IsZero() {
		t.Fatalf("now=%s", got)
	}
	m.Set(time.Now())
	m.Advance(time.Second)
}

func TestManualConcurrent(t *testing.T) {
	m := clock.NewManual(time.Unix(0, 0).UTC())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.Advance(time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = m.Now()
			}
		}()
	}
	wg.Wait()
	if want := time.Unix(0, 0).UTC().Add(800 * time.Millisecond); !m.Now().Equal(want) {
		t.Fatalf("now=%s want=%s", m.Now(), want)
	}
}
