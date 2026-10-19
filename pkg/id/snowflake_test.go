package id_test

import (
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/id"
)

func TestSnowflakeUniqueMonotonic(t *testing.T) {
	clock := id.NewManualClock(time.UnixMilli(id.DefaultEpochMS + 1000).UTC())
	gen, err := id.NewSnowflake(id.Options{NodeID: 7, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}

	var prev int64
	seen := make(map[int64]struct{}, 5000)
	for i := 0; i < 5000; i++ {
		if i > 0 && i%100 == 0 {
			clock.Advance(time.Millisecond)
		}
		n, err := gen.Next()
		if err != nil {
			t.Fatal(err)
		}
		if n <= prev {
			t.Fatalf("not increasing: prev=%d n=%d", prev, n)
		}
		if _, ok := seen[n]; ok {
			t.Fatalf("duplicate %d", n)
		}
		seen[n] = struct{}{}
		prev = n

		p := id.Parse(n, gen.EpochMS())
		if p.NodeID != 7 {
			t.Fatalf("node=%d", p.NodeID)
		}
	}
}

func TestSnowflakeConcurrentUnique(t *testing.T) {
	gen := id.MustSnowflake(id.Options{NodeID: 1})
	const workers = 8
	const per = 1000
	ids := make(chan int64, workers*per)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				n, err := gen.Next()
				if err != nil {
					t.Errorf("next: %v", err)
					return
				}
				ids <- n
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]struct{}, workers*per)
	for n := range ids {
		if _, ok := seen[n]; ok {
			t.Fatalf("duplicate %d", n)
		}
		seen[n] = struct{}{}
	}
	if len(seen) != workers*per {
		t.Fatalf("len=%d", len(seen))
	}
}

func TestSnowflakeInvalidNode(t *testing.T) {
	_, err := id.NewSnowflake(id.Options{NodeID: 1024})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*id.ErrInvalidNode); !ok {
		t.Fatalf("err=%T %v", err, err)
	}
}

func TestSnowflakeClockRollback(t *testing.T) {
	clock := id.NewManualClock(time.UnixMilli(id.DefaultEpochMS + 10_000).UTC())
	gen := id.MustSnowflake(id.Options{
		NodeID:      1,
		Clock:       clock,
		MaxBackward: -1, // 不容忍回拨
	})
	if _, err := gen.Next(); err != nil {
		t.Fatal(err)
	}
	clock.Set(time.UnixMilli(id.DefaultEpochMS + 9_000).UTC())
	_, err := gen.Next()
	var ce *id.ErrClockRollback
	if err == nil {
		t.Fatal("expected rollback error")
	}
	if e, ok := err.(*id.ErrClockRollback); !ok {
		t.Fatalf("err=%T %v", err, err)
	} else {
		ce = e
	}
	if ce.Backward < time.Second {
		t.Fatalf("backward=%s", ce.Backward)
	}
}

func TestSnowflakeNextStringAndParse(t *testing.T) {
	gen := id.MustSnowflake(id.Options{NodeID: 3})
	s, err := gen.NextString()
	if err != nil {
		t.Fatal(err)
	}
	p, err := id.ParseString(s, gen.EpochMS())
	if err != nil {
		t.Fatal(err)
	}
	if p.NodeID != 3 {
		t.Fatalf("node=%d", p.NodeID)
	}
}

func TestSnowflakeSequenceWithinMillis(t *testing.T) {
	clock := id.NewManualClock(time.UnixMilli(id.DefaultEpochMS + 100).UTC())
	gen := id.MustSnowflake(id.Options{NodeID: 0, Clock: clock})

	seqs := make(map[int64]struct{}, 4096)
	for i := 0; i < 4096; i++ {
		n, err := gen.Next()
		if err != nil {
			t.Fatalf("i=%d err=%v", i, err)
		}
		p := id.Parse(n, gen.EpochMS())
		if p.TimestampMS != 100 {
			t.Fatalf("ts=%d", p.TimestampMS)
		}
		seqs[p.Sequence] = struct{}{}
	}
	if len(seqs) != 4096 {
		t.Fatalf("unique seq=%d", len(seqs))
	}

	// 序列耗尽后应进入下一毫秒（由可推进时钟驱动 waitNextMS）
	clock2 := id.NewManualClock(time.UnixMilli(id.DefaultEpochMS + 200).UTC())
	gen2 := id.MustSnowflake(id.Options{NodeID: 1, Clock: clock2})
	for i := 0; i < 4096; i++ {
		if _, err := gen2.Next(); err != nil {
			t.Fatal(err)
		}
	}
	errCh := make(chan error, 1)
	idCh := make(chan int64, 1)
	go func() {
		n, err := gen2.Next()
		idCh <- n
		errCh <- err
	}()
	// 等待进入自旋后再拨表，避免竞态漏测
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		clock2.Advance(time.Millisecond)
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err)
			}
			p := id.Parse(<-idCh, gen2.EpochMS())
			if p.Sequence != 0 || p.TimestampMS < 201 {
				t.Fatalf("parts=%+v", p)
			}
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("timeout waiting for sequence rollover")
}
