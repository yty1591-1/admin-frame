package mq_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/mq"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

func TestMemoryPublishSubscribe(t *testing.T) {
	broker := mq.NewMemory(mq.MemoryConfig{})
	defer func() { _ = broker.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var got atomic.Value
	err := broker.Consumer().Subscribe("order.created", func(_ context.Context, msg mq.Message) error {
		got.Store(string(msg.Body))
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = broker.Consumer().Run(ctx) }()

	if err := broker.Producer().Publish(context.Background(), mq.Message{
		Topic:   "order.created",
		Body:    []byte(`{"id":1}`),
		Headers: map[string]string{mq.HeaderSchemaVersion: "v1", mq.HeaderMerchantID: "m1"},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if got.Load().(string) != `{"id":1}` {
		t.Fatalf("got=%v", got.Load())
	}
}

func TestWrapRetryThenAck(t *testing.T) {
	var attempts atomic.Int32
	h := mq.Wrap(func(_ context.Context, _ mq.Message) error {
		n := attempts.Add(1)
		if n < 3 {
			return pkgerrors.Unavailable("tmp")
		}
		return nil
	}, mq.ConsumeOptions{MaxAttempts: 5})

	broker := mq.NewMemory(mq.MemoryConfig{})
	defer func() { _ = broker.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = broker.Consumer().Subscribe("t", func(ctx context.Context, msg mq.Message) error {
		err := h(ctx, msg)
		if err == nil {
			cancel()
		}
		return err
	})
	go func() { _ = broker.Consumer().Run(ctx) }()
	_ = broker.Producer().Publish(context.Background(), mq.Message{Topic: "t", Body: []byte("x")})

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestWrapDeadLetterOnPermanent(t *testing.T) {
	var dlqMsg atomic.Value
	h := mq.Wrap(func(_ context.Context, _ mq.Message) error {
		return retry.Permanent(errors.New("bad payload"))
	}, mq.ConsumeOptions{
		MaxAttempts: 3,
		DeadLetter: mq.DeadLetterFunc(func(_ context.Context, msg mq.Message, _ error) error {
			dlqMsg.Store(msg.ID)
			return nil
		}),
	})

	broker := mq.NewMemory(mq.MemoryConfig{})
	defer func() { _ = broker.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = broker.Consumer().Subscribe("t", func(ctx context.Context, msg mq.Message) error {
		err := h(ctx, msg)
		cancel()
		return err
	})
	go func() { _ = broker.Consumer().Run(ctx) }()
	_ = broker.Producer().Publish(context.Background(), mq.Message{Topic: "t", Body: []byte("x")})

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if dlqMsg.Load() == nil {
		t.Fatal("expected dlq")
	}
}

func TestClassify(t *testing.T) {
	opts := mq.ConsumeOptions{MaxAttempts: 3}
	if mq.Classify(nil, 1, opts) != mq.DispositionAck {
		t.Fatal("ack")
	}
	if mq.Classify(pkgerrors.Unavailable("x"), 1, opts) != mq.DispositionRetry {
		t.Fatal("retry")
	}
	if mq.Classify(pkgerrors.Unavailable("x"), 3, opts) != mq.DispositionDeadLetter {
		t.Fatal("dlq max")
	}
	if mq.Classify(retry.Permanent(errors.New("x")), 1, opts) != mq.DispositionDeadLetter {
		t.Fatal("dlq permanent")
	}
	if mq.Classify(pkgerrors.NotFound("x"), 1, opts) != mq.DispositionDeadLetter {
		t.Fatal("dlq not retryable")
	}
}

func TestMiddlewareChain(t *testing.T) {
	var order []int
	h := mq.Chain(func(_ context.Context, _ mq.Message) error {
		order = append(order, 0)
		return nil
	}, func(next mq.Handler) mq.Handler {
		return func(ctx context.Context, msg mq.Message) error {
			order = append(order, 1)
			return next(ctx, msg)
		}
	}, func(next mq.Handler) mq.Handler {
		return func(ctx context.Context, msg mq.Message) error {
			order = append(order, 2)
			return next(ctx, msg)
		}
	})
	_ = h(context.Background(), mq.Message{Topic: "t"})
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 0 {
		t.Fatalf("order=%v", order)
	}
}

func TestMessageHelpers(t *testing.T) {
	m := mq.Message{Topic: "t"}.WithHeader(mq.HeaderMerchantID, "m9").WithHeader(mq.HeaderSchemaVersion, "v2")
	if m.MerchantID() != "m9" || m.SchemaVersion() != "v2" {
		t.Fatalf("%+v", m)
	}
	if err := (mq.Message{}).Validate(); !errors.Is(err, mq.ErrInvalidMessage) {
		t.Fatalf("err=%v", err)
	}
}
