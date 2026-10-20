// Package smstest 提供短信驱动的契约测试，供各驱动与自定义实现复用。
//
// 用法：
//
//	func TestContract(t *testing.T) {
//		smstest.Run(t, func(t *testing.T) sms.Sender {
//			return sms.NewMemory(sms.MemoryConfig{})
//		})
//	}
package smstest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/sms"
)

// ValidMessage 返回一条结构合法、可直接发送的短信。
func ValidMessage() sms.Message {
	return sms.Message{
		Phone:          "+8613800138000",
		SignName:       "测试签名",
		TemplateID:     "SMS_000001",
		Params:         map[string]string{"code": "1234"},
		Locale:         "zh-CN",
		IdempotencyKey: "sms:contract:1",
		Reference:      "order-1",
	}
}

// Run 对 Sender 执行统一契约测试；newSender 必须返回可用实例，Run 负责 Close。
func Run(t *testing.T, newSender func(t *testing.T) sms.Sender) {
	t.Helper()
	if newSender == nil {
		t.Fatal("smstest: newSender is nil")
	}

	t.Run("SendSuccess", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		receipt, err := sender.Send(context.Background(), ValidMessage())
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		if receipt.Provider == "" {
			t.Fatal("receipt provider is empty")
		}
		if receipt.AcceptedAt.IsZero() {
			t.Fatal("receipt accepted_at is zero")
		}
		if receipt.MessageID == "" {
			t.Fatal("receipt message id is empty")
		}
	})

	t.Run("InvalidRecipient", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Phone = "13800138000"
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, sms.ErrInvalidRecipient) {
			t.Fatalf("err=%v want ErrInvalidRecipient", err)
		}
	})

	t.Run("BothTemplateAndContent", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Content = "hello"
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, sms.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("ControlCharacters", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Params = map[string]string{"code": "12\r\n34"}
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, sms.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("CanceledContext", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := sender.Send(ctx, ValidMessage())
		if err == nil {
			t.Fatal("expected canceled error")
		}
		if !errors.Is(err, context.Canceled) && !pkgerrors.IsCode(err, pkgerrors.CodeCanceled) {
			t.Fatalf("err=%v want context.Canceled or CodeCanceled", err)
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
		if _, err := sender.Send(context.Background(), ValidMessage()); !errors.Is(err, sms.ErrClosed) {
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

	t.Run("ErrorDoesNotLeakRecipient", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Phone = "+8613800138000"
		msg.TemplateID = ""
		_, err := sender.Send(context.Background(), msg)
		if err == nil {
			t.Fatal("expected error")
		}
		if strings.Contains(err.Error(), "13800138000") {
			t.Fatalf("error leaks recipient: %v", err)
		}
	})

	t.Run("TimeoutBudget", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := sender.Send(ctx, ValidMessage()); err != nil {
			t.Fatalf("send: %v", err)
		}
	})
}
