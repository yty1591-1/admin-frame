// Package pushtest 提供推送驱动的契约测试，供各驱动与自定义实现复用。
//
// 用法：
//
//	func TestContract(t *testing.T) {
//		pushtest.Run(t, func(t *testing.T) push.Sender {
//			return push.NewMemory(push.MemoryConfig{})
//		})
//	}
package pushtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/push"
)

// ValidToken 返回一个结构合法、长度符合要求的推送 Token。
func ValidToken() string { return strings.Repeat("ab", 32) }

// ValidMessage 返回一条结构合法、可直接发送的推送。
func ValidMessage() push.Message {
	badge := 1
	return push.Message{
		Token:          ValidToken(),
		Platform:       push.PlatformIOS,
		Title:          "订单更新",
		Body:           "您的订单已发货",
		Data:           map[string]string{"order_id": "123", "type": "shipping"},
		Badge:          &badge,
		Sound:          "default",
		CollapseKey:    "order-123",
		TTL:            time.Hour,
		Priority:       push.PriorityHigh,
		IdempotencyKey: "push:contract:1",
		Reference:      "order-123",
	}
}

// Run 对 Sender 执行统一契约测试；newSender 必须返回可用实例，Run 负责 Close。
func Run(t *testing.T, newSender func(t *testing.T) push.Sender) {
	t.Helper()
	if newSender == nil {
		t.Fatal("pushtest: newSender is nil")
	}

	t.Run("SendSuccess", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		receipt, err := sender.Send(context.Background(), ValidMessage())
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if receipt.Provider == "" || receipt.MessageID == "" {
			t.Fatalf("unexpected receipt: %+v", receipt)
		}
		if receipt.AcceptedAt.IsZero() {
			t.Fatal("accepted_at is zero")
		}
	})

	t.Run("InvalidToken", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Token = "short"
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrInvalidRecipient) {
			t.Fatalf("err=%v want ErrInvalidRecipient", err)
		}
	})

	t.Run("InvalidPlatform", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Platform = "symbian"
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("MissingContent", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Title = ""
		msg.Body = ""
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("ReservedDataKey", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Data = map[string]string{"aps": "x"}
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("PayloadTooLarge", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Data = make(map[string]string, 5)
		for i := 0; i < 5; i++ {
			msg.Data[string(rune('a'+i))] = strings.Repeat("x", 1000)
		}
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("ControlCharacter", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Title = "a\r\nb"
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, push.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("CanceledContext", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := sender.Send(ctx, ValidMessage())
		if !errors.Is(err, context.Canceled) && !pkgerrors.IsCode(err, pkgerrors.CodeCanceled) {
			t.Fatalf("err=%v want canceled", err)
		}
	})

	t.Run("Closed", func(t *testing.T) {
		sender := newSender(t)
		if err := sender.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := sender.Close(); err != nil {
			t.Fatalf("second close: %v", err)
		}
		if _, err := sender.Send(context.Background(), ValidMessage()); !errors.Is(err, push.ErrClosed) {
			t.Fatalf("err=%v want ErrClosed", err)
		}
	})

	t.Run("Concurrent", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		const workers = 32
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := sender.Send(context.Background(), ValidMessage()); err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent send: %v", err)
		}
	})

	t.Run("ErrorDoesNotLeakToken", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Platform = "symbian"
		_, err := sender.Send(context.Background(), msg)
		if err == nil {
			t.Fatal("expected error")
		}
		if strings.Contains(err.Error(), msg.Token) {
			t.Fatalf("error leaks token: %v", err)
		}
	})
}
