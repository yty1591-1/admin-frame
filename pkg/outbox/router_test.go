package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/outbox"
	"github.com/admin-frame/admin-frame/pkg/retry"
)

func TestRouterRegisterValidation(t *testing.T) {
	router := outbox.NewRouter()
	if err := router.Register(" ", func(context.Context, *outbox.Event) error { return nil }); err == nil {
		t.Fatal("expected error for empty topic")
	}
	if err := router.Register("demo.created", nil); err == nil {
		t.Fatal("expected error for nil handler")
	}
	handler := func(context.Context, *outbox.Event) error { return nil }
	if err := router.Register("demo.created", handler); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := router.Register("demo.created", handler); err == nil {
		t.Fatal("expected error for duplicate topic")
	}
	if !router.Has("demo.created") || router.Has("demo.missing") {
		t.Fatal("Has mismatch")
	}
	topics := router.Topics()
	if len(topics) != 1 || topics[0] != "demo.created" {
		t.Fatalf("Topics = %v", topics)
	}
}

func TestRouterDispatch(t *testing.T) {
	router := outbox.NewRouter()
	var received string
	if err := router.Register("demo.created", func(_ context.Context, event *outbox.Event) error {
		received = event.EventID
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	event := newEvent(t, "evt-router")
	if err := router.Handler()(context.Background(), event); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if received != "evt-router" {
		t.Fatalf("handler received %q", received)
	}
}

func TestRouterUnknownTopicIsPermanent(t *testing.T) {
	router := outbox.NewRouter()
	event := newEvent(t, "evt-unknown")
	err := router.Handler()(context.Background(), event)
	if err == nil {
		t.Fatal("expected error for unknown topic")
	}
	if !errors.Is(err, outbox.ErrUnknownTopic) {
		t.Fatalf("expected ErrUnknownTopic, got %v", err)
	}
	if !retry.IsPermanent(err) {
		t.Fatal("unknown topic must be permanent so it lands in DLQ")
	}
}
