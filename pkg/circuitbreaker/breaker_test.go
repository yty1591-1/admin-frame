package circuitbreaker_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	"github.com/admin-frame/admin-frame/pkg/clock"
)

func newTestBreaker(t *testing.T, clk *clock.Manual, opts ...func(*circuitbreaker.Config)) *circuitbreaker.Breaker {
	t.Helper()
	cfg := circuitbreaker.Config{
		Name:        "test",
		MaxRequests: 1,
		Timeout:     time.Second,
		Clock:       clk,
		ReadyToTrip: func(c circuitbreaker.Counts) bool { return c.ConsecutiveFailures >= 2 },
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	b, err := circuitbreaker.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNewInvalidConfig(t *testing.T) {
	if _, err := circuitbreaker.New(circuitbreaker.Config{Timeout: -time.Second}); !errors.Is(err, circuitbreaker.ErrInvalidConfig) {
		t.Fatalf("err=%v", err)
	}
	if _, err := circuitbreaker.New(circuitbreaker.Config{Interval: -time.Second}); !errors.Is(err, circuitbreaker.ErrInvalidConfig) {
		t.Fatalf("err=%v", err)
	}
	cfg := circuitbreaker.DefaultConfig()
	if cfg.Name == "" || cfg.MaxRequests == 0 || cfg.Timeout <= 0 || cfg.Clock == nil {
		t.Fatalf("default config=%+v", cfg)
	}
}

func TestClosedToOpenAndReject(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk)

	if err := b.Do(func() error { return errors.New("boom") }); err == nil {
		t.Fatal("expected error")
	}
	if b.State() != circuitbreaker.StateClosed {
		t.Fatalf("state=%s", b.State())
	}
	if err := b.Do(func() error { return errors.New("boom") }); err == nil {
		t.Fatal("expected error")
	}
	if b.State() != circuitbreaker.StateOpen {
		t.Fatalf("state=%s", b.State())
	}

	called := false
	err := b.Do(func() error {
		called = true
		return nil
	})
	if !errors.Is(err, circuitbreaker.ErrOpen) {
		t.Fatalf("err=%v", err)
	}
	if called {
		t.Fatal("fn must not be called while open")
	}
}

func TestOpenToHalfOpenToClosed(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk)
	trip(t, b)

	clk.Advance(time.Second)
	if state := b.State(); state != circuitbreaker.StateHalfOpen {
		t.Fatalf("state=%s", state)
	}
	if err := b.Do(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if state := b.State(); state != circuitbreaker.StateClosed {
		t.Fatalf("state=%s", state)
	}
	if err := b.Do(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk)
	trip(t, b)

	clk.Advance(time.Second)
	if err := b.Do(func() error { return errors.New("probe failed") }); err == nil {
		t.Fatal("expected error")
	}
	if state := b.State(); state != circuitbreaker.StateOpen {
		t.Fatalf("state=%s", state)
	}
}

func TestHalfOpenMaxRequests(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk, func(c *circuitbreaker.Config) {
		c.MaxRequests = 1
	})
	trip(t, b)
	clk.Advance(time.Second)

	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = b.Do(func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	err := b.Do(func() error { return nil })
	if !errors.Is(err, circuitbreaker.ErrTooManyRequests) {
		t.Fatalf("err=%v", err)
	}
	close(release)
	wg.Wait()
	if state := b.State(); state != circuitbreaker.StateClosed {
		t.Fatalf("state=%s", state)
	}
}

func TestIntervalResetsCounts(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk, func(c *circuitbreaker.Config) {
		c.Interval = 100 * time.Millisecond
	})
	_ = b.Do(func() error { return errors.New("first") })
	if b.State() != circuitbreaker.StateClosed {
		t.Fatalf("state=%s", b.State())
	}
	clk.Advance(100 * time.Millisecond)
	_ = b.Do(func() error { return errors.New("after reset") })
	if b.State() != circuitbreaker.StateClosed {
		t.Fatalf("state=%s after first failure in new window", b.State())
	}
	_ = b.Do(func() error { return errors.New("second") })
	if b.State() != circuitbreaker.StateOpen {
		t.Fatalf("state=%s", b.State())
	}
}

func TestIsSuccessfulOverride(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk, func(c *circuitbreaker.Config) {
		c.ReadyToTrip = func(circuitbreaker.Counts) bool { return true }
		c.IsSuccessful = func(err error) bool {
			return err == nil || errors.Is(err, errIgnored)
		}
	})
	if err := b.Do(func() error { return errIgnored }); !errors.Is(err, errIgnored) {
		t.Fatalf("err=%v", err)
	}
	if state := b.State(); state != circuitbreaker.StateClosed {
		t.Fatalf("state=%s", state)
	}
}

var errIgnored = errors.New("ignored")

func TestPanicCountsAsFailureAndPropagates(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk, func(c *circuitbreaker.Config) {
		c.ReadyToTrip = func(counts circuitbreaker.Counts) bool { return counts.ConsecutiveFailures >= 1 }
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = b.Do(func() error { panic("boom") })
	}()
	if state := b.State(); state != circuitbreaker.StateOpen {
		t.Fatalf("state=%s", state)
	}
}

func TestCounts(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk, func(c *circuitbreaker.Config) {
		c.ReadyToTrip = func(circuitbreaker.Counts) bool { return false }
	})
	_ = b.Do(func() error { return nil })
	_ = b.Do(func() error { return errors.New("boom") })
	_ = b.Do(func() error { return nil })
	c := b.Counts()
	if c.Requests != 3 || c.TotalSuccesses != 2 || c.TotalFailures != 1 {
		t.Fatalf("counts=%+v", c)
	}
	if c.ConsecutiveSuccesses != 1 || c.ConsecutiveFailures != 0 {
		t.Fatalf("counts=%+v", c)
	}
}

func TestOnStateChange(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	var mu sync.Mutex
	var events []string
	b := newTestBreaker(t, clk, func(c *circuitbreaker.Config) {
		c.OnStateChange = func(name string, from, to circuitbreaker.State) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, name+":"+from.String()+"->"+to.String())
		}
	})
	trip(t, b)
	clk.Advance(time.Second)
	_ = b.Do(func() error { return nil })
	_ = b.Do(func() error { return nil })

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"test:closed->open",
		"test:open->half-open",
		"test:half-open->closed",
	}
	if len(events) != len(want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
}

func TestDoValue(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk)
	v, err := circuitbreaker.DoValue(b, func() (string, error) { return "ok", nil })
	if err != nil || v != "ok" {
		t.Fatalf("v=%q err=%v", v, err)
	}
}

func TestNilInputs(t *testing.T) {
	b := newTestBreaker(t, clock.NewManual(time.Time{}))
	if err := b.Do(nil); !errors.Is(err, circuitbreaker.ErrNilFunc) {
		t.Fatalf("err=%v", err)
	}
	if _, err := circuitbreaker.DoValue[string](nil, func() (string, error) { return "", nil }); !errors.Is(err, circuitbreaker.ErrNilBreaker) {
		t.Fatalf("err=%v", err)
	}
	var nilBreaker *circuitbreaker.Breaker
	if err := nilBreaker.Do(func() error { return nil }); !errors.Is(err, circuitbreaker.ErrNilBreaker) {
		t.Fatalf("err=%v", err)
	}
}

func TestReset(t *testing.T) {
	clk := clock.NewManual(time.Unix(0, 0).UTC())
	b := newTestBreaker(t, clk)
	trip(t, b)
	b.Reset()
	if state := b.State(); state != circuitbreaker.StateClosed {
		t.Fatalf("state=%s", state)
	}
	if c := b.Counts(); c != (circuitbreaker.Counts{}) {
		t.Fatalf("counts=%+v", c)
	}
}

func trip(t *testing.T, b *circuitbreaker.Breaker) {
	t.Helper()
	_ = b.Do(func() error { return errors.New("boom") })
	_ = b.Do(func() error { return errors.New("boom") })
	if state := b.State(); state != circuitbreaker.StateOpen {
		t.Fatalf("state=%s", state)
	}
}
