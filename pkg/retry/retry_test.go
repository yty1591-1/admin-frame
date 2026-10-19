package retry_test

import (
	"context"
	stderrors "errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

func TestDoSucceedsFirstTry(t *testing.T) {
	var n int
	err := retry.Do(context.Background(), retry.DefaultPolicy(), func(context.Context) error {
		n++
		return nil
	})
	if err != nil || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestDoRetriesRetryableThenSucceeds(t *testing.T) {
	var n atomic.Int32
	p := retry.NewPolicy(
		retry.WithMaxAttempts(5),
		retry.WithBackoff(time.Millisecond, 5*time.Millisecond),
		retry.WithJitterFraction(0),
	)
	err := retry.Do(context.Background(), p, func(context.Context) error {
		if n.Add(1) < 3 {
			return errors.Unavailable("down")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Fatalf("n=%d", n.Load())
	}
}

func TestDoDoesNotRetryNonRetryable(t *testing.T) {
	var n int
	err := retry.Do(context.Background(), retry.DefaultPolicy(), func(context.Context) error {
		n++
		return errors.NotFound("missing")
	})
	if !errors.IsCode(err, errors.CodeNotFound) {
		t.Fatalf("err=%v", err)
	}
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
}

func TestDoPermanentStops(t *testing.T) {
	var n int
	root := errors.Unavailable("down")
	err := retry.Do(context.Background(), retry.NewPolicy(
		retry.WithMaxAttempts(5),
		retry.WithRetryIf(func(error) bool { return true }),
	), func(context.Context) error {
		n++
		return retry.Permanent(root)
	})
	if !stderrors.Is(err, root) {
		t.Fatalf("err=%v", err)
	}
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	if retry.IsPermanent(err) {
		t.Fatal("returned error should be unwrapped")
	}
}

func TestDoExhausted(t *testing.T) {
	p := retry.NewPolicy(
		retry.WithMaxAttempts(3),
		retry.WithBackoff(time.Millisecond, time.Millisecond),
		retry.WithJitterFraction(0),
	)
	err := retry.Do(context.Background(), p, func(context.Context) error {
		return errors.Unavailable("down")
	})
	if !retry.IsExhausted(err) {
		t.Fatalf("err=%v", err)
	}
	var ee *retry.ExhaustedError
	if !stderrors.As(err, &ee) || ee.Attempts != 3 {
		t.Fatalf("ee=%+v", ee)
	}
	if !errors.IsCode(err, errors.CodeUnavailable) {
		t.Fatalf("unwrap code missing: %v", err)
	}
}

func TestDoRespectsCancelDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := retry.NewPolicy(
		retry.WithMaxAttempts(5),
		retry.WithBackoff(time.Hour, time.Hour),
		retry.WithJitterFraction(0),
		retry.WithOnRetry(func(int, error, time.Duration) { cancel() }),
	)
	err := retry.Do(ctx, p, func(context.Context) error {
		return errors.Unavailable("down")
	})
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestDoValue(t *testing.T) {
	p := retry.NewPolicy(
		retry.WithMaxAttempts(3),
		retry.WithBackoff(time.Millisecond, time.Millisecond),
		retry.WithJitterFraction(0),
	)
	var n int
	v, err := retry.DoValue(context.Background(), p, func(context.Context) (string, error) {
		n++
		if n < 2 {
			return "", errors.ResourceExhausted("busy")
		}
		return "ok", nil
	})
	if err != nil || v != "ok" || n != 2 {
		t.Fatalf("v=%q err=%v n=%d", v, err, n)
	}
}

func TestDoMaxElapsed(t *testing.T) {
	p := retry.NewPolicy(
		retry.WithMaxAttempts(50),
		retry.WithMaxElapsed(20*time.Millisecond),
		retry.WithBackoff(5*time.Millisecond, 5*time.Millisecond),
		retry.WithJitterFraction(0),
	)
	err := retry.Do(context.Background(), p, func(context.Context) error {
		return errors.Unavailable("down")
	})
	if !retry.IsExhausted(err) {
		t.Fatalf("err=%v", err)
	}
}

func TestDefaultRetryIfUnknown(t *testing.T) {
	if retry.DefaultRetryIf(stderrors.New("plain")) {
		t.Fatal("unknown should not retry")
	}
	if !retry.DefaultRetryIf(errors.Unavailable("x")) {
		t.Fatal("unavailable should retry")
	}
}

func TestOnRetryCalled(t *testing.T) {
	var delays []time.Duration
	p := retry.NewPolicy(
		retry.WithMaxAttempts(3),
		retry.WithBackoff(2*time.Millisecond, 10*time.Millisecond),
		retry.WithJitterFraction(0),
		retry.WithMultiplier(2),
		retry.WithOnRetry(func(_ int, _ error, d time.Duration) {
			delays = append(delays, d)
		}),
	)
	_ = retry.Do(context.Background(), p, func(context.Context) error {
		return errors.Unavailable("down")
	})
	if len(delays) != 2 {
		t.Fatalf("delays=%v", delays)
	}
	if delays[0] != 2*time.Millisecond || delays[1] != 4*time.Millisecond {
		t.Fatalf("delays=%v", delays)
	}
}
