// Package emailtest 提供邮件驱动的契约测试，供各驱动与自定义实现复用。
//
// 用法：
//
//	func TestContract(t *testing.T) {
//		emailtest.Run(t, func(t *testing.T) email.Sender {
//			return email.NewMemory(email.MemoryConfig{})
//		})
//	}
package emailtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/email"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

// ValidMessage 返回一封结构合法、可直接发送的邮件。
func ValidMessage() email.Message {
	return email.Message{
		From:    email.Address{Name: "平台", Email: "noreply@example.com"},
		To:      []email.Address{{Name: "用户", Email: "user@example.com"}},
		Cc:      []email.Address{{Email: "cc@example.com"}},
		Bcc:     []email.Address{{Email: "bcc@example.com"}},
		Subject: "您的验证码",
		Text:    "验证码：123456",
		HTML:    "<p>验证码：123456</p>",
		Attachments: []email.Attachment{
			{Filename: "report.txt", ContentType: "text/plain", Data: []byte("hello")},
		},
		IdempotencyKey: "email:contract:1",
		Reference:      "export-1",
	}
}

// Run 对 Sender 执行统一契约测试；newSender 必须返回可用实例，Run 负责 Close。
func Run(t *testing.T, newSender func(t *testing.T) email.Sender) {
	t.Helper()
	if newSender == nil {
		t.Fatal("emailtest: newSender is nil")
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
		if receipt.MessageID == "" {
			t.Fatal("receipt message id is empty")
		}
		if receipt.AcceptedAt.IsZero() {
			t.Fatal("receipt accepted_at is zero")
		}
	})

	t.Run("InvalidRecipient", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.To = []email.Address{{Email: "not-an-email"}}
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, email.ErrInvalidRecipient) {
			t.Fatalf("err=%v want ErrInvalidRecipient", err)
		}
	})

	t.Run("MissingBody", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Text = ""
		msg.HTML = ""
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, email.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("SubjectInjection", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Subject = "hello\r\nBcc: attacker@example.com"
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, email.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("HeaderNotAllowed", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Headers = map[string]string{"Bcc": "attacker@example.com"}
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, email.ErrInvalidMessage) {
			t.Fatalf("err=%v want ErrInvalidMessage", err)
		}
	})

	t.Run("ExecutableAttachment", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		msg := ValidMessage()
		msg.Attachments = []email.Attachment{{Filename: "payload.exe", Data: []byte("MZ")}}
		if _, err := sender.Send(context.Background(), msg); !errors.Is(err, email.ErrRejected) {
			t.Fatalf("err=%v want ErrRejected", err)
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
		if _, err := sender.Send(context.Background(), ValidMessage()); !errors.Is(err, email.ErrClosed) {
			t.Fatalf("err=%v want ErrClosed", err)
		}
	})

	t.Run("Concurrent", func(t *testing.T) {
		sender := newSender(t)
		t.Cleanup(func() { _ = sender.Close() })
		const workers = 16
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
		msg.To = []email.Address{{Email: "user@example.com"}}
		msg.Text = ""
		msg.HTML = ""
		_, err := sender.Send(context.Background(), msg)
		if err == nil {
			t.Fatal("expected error")
		}
		if strings.Contains(err.Error(), "user@example.com") {
			t.Fatalf("error leaks recipient: %v", err)
		}
	})
}
