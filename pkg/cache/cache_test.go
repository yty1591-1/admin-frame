package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/cache"
)

type manualClock struct{ t time.Time }

func (c *manualClock) Now() time.Time          { return c.t }
func (c *manualClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestMemoryLRUAndTTL(t *testing.T) {
	clk := &manualClock{t: time.Unix(1_700_000_000, 0).UTC()}
	m := cache.NewMemory(cache.MemoryConfig{MaxEntries: 2, Clock: clk})
	ctx := context.Background()

	if err := m.Set(ctx, "a", []byte("1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(ctx, "b", []byte("2"), time.Minute); err != nil {
		t.Fatal(err)
	}
	// touch a so b is LRU victim when c arrives? Actually Set b then Set a moves a front; insert c evicts back.
	_, _ = m.Get(ctx, "a")
	if err := m.Set(ctx, "c", []byte("3"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, "b"); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("b should be evicted, err=%v", err)
	}
	got, err := m.Get(ctx, "a")
	if err != nil || string(got) != "1" {
		t.Fatalf("a=%q err=%v", got, err)
	}

	clk.advance(2 * time.Minute)
	if _, err := m.Get(ctx, "a"); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("expired err=%v", err)
	}
}

func TestMemoryNegative(t *testing.T) {
	m := cache.NewMemory(cache.MemoryConfig{MaxEntries: 8})
	ctx := context.Background()
	if err := m.SetNegative(ctx, "missing", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, "missing"); !errors.Is(err, cache.ErrNegative) {
		t.Fatalf("err=%v", err)
	}
}

func TestMultiL1L2(t *testing.T) {
	l1 := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16})
	l2 := cache.NewMemory(cache.MemoryConfig{MaxEntries: 16})
	multi, err := cache.NewMulti(cache.MultiConfig{
		Layers: []cache.Store{l1, l2},
		L1TTL:  30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := l2.Set(ctx, "k", []byte("from-l2"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := multi.Get(ctx, "k")
	if err != nil || string(got) != "from-l2" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	// L1 should be backfilled
	got, err = l1.Get(ctx, "k")
	if err != nil || string(got) != "from-l2" {
		t.Fatalf("l1 backfill=%q err=%v", got, err)
	}

	if err := multi.Set(ctx, "n", []byte("both"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := l1.Get(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l2.Get(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if err := multi.Delete(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l1.Get(ctx, "n"); !errors.Is(err, cache.ErrMiss) {
		t.Fatal(err)
	}
}

func TestAsideGetOrLoadCoalesceAndNegative(t *testing.T) {
	mem := cache.NewMemory(cache.MemoryConfig{MaxEntries: 64})
	c, err := cache.New(mem,
		cache.WithTTL(time.Minute),
		cache.WithNegativeTTL(10*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	var loads atomic.Int32
	var wg sync.WaitGroup
	const n = 16
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			val, err := c.GetOrLoad(ctx, "hot", func(ctx context.Context) ([]byte, error) {
				loads.Add(1)
				time.Sleep(15 * time.Millisecond)
				return []byte("ok"), nil
			})
			if err != nil || string(val) != "ok" {
				t.Errorf("val=%q err=%v", val, err)
			}
		}()
	}
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("loads=%d", loads.Load())
	}

	_, err = c.GetOrLoad(ctx, "none", func(ctx context.Context) ([]byte, error) {
		return nil, nil
	})
	if !errors.Is(err, cache.ErrNegative) {
		t.Fatalf("err=%v", err)
	}
	// second call should hit negative without loader
	_, err = c.GetOrLoad(ctx, "none", func(ctx context.Context) ([]byte, error) {
		t.Fatal("loader should not run")
		return nil, nil
	})
	if !errors.Is(err, cache.ErrNegative) {
		t.Fatalf("err=%v", err)
	}
}

func TestAsideRequiresTTL(t *testing.T) {
	c, err := cache.New(cache.NewMemory(cache.MemoryConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetOrLoad(context.Background(), "k", func(ctx context.Context) ([]byte, error) {
		return []byte("x"), nil
	})
	if !errors.Is(err, cache.ErrInvalidConfig) {
		t.Fatalf("err=%v", err)
	}
}

func TestJitterIncreasesTTL(t *testing.T) {
	clk := &manualClock{t: time.Unix(1_700_000_000, 0).UTC()}
	mem := cache.NewMemory(cache.MemoryConfig{MaxEntries: 8, Clock: clk})
	c, err := cache.New(mem, cache.WithTTL(time.Minute), cache.WithJitter(30*time.Second), cache.WithClock(clk))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Set(ctx, "j", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	// without jitter would expire at +1m; with jitter still alive at +1m - 1s?
	// Actually jitter ADDS time, so at +61s might still be alive. At +60s exactly expiresAt is now+60s+extra.
	clk.advance(time.Minute)
	// still may be alive due to jitter
	_, err = mem.Get(ctx, "j")
	if err != nil && !errors.Is(err, cache.ErrMiss) {
		t.Fatal(err)
	}
	clk.advance(40 * time.Second)
	if _, err := mem.Get(ctx, "j"); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("should expire after base+jitter, err=%v", err)
	}
}

func TestMultiSoftFail(t *testing.T) {
	l1 := cache.NewMemory(cache.MemoryConfig{MaxEntries: 8})
	bad := cache.FuncStore{
		GetFunc: func(ctx context.Context, key string) ([]byte, error) {
			return nil, errors.New("redis down")
		},
		SetFunc: func(ctx context.Context, key string, value []byte, ttl time.Duration) error {
			return errors.New("redis down")
		},
		DeleteFunc: func(ctx context.Context, keys ...string) error {
			return errors.New("redis down")
		},
	}
	multi, err := cache.NewMulti(cache.MultiConfig{
		Layers:   []cache.Store{l1, bad},
		SoftFail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := multi.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := multi.Get(ctx, "k")
	if err != nil || string(got) != "v" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
