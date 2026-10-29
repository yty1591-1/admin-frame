package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/circuitbreaker"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"
)

// fakeSender 记录最后一条消息并可注入错误。
type fakeSender struct {
	msg   pkgsms.Message
	err   error
	calls int
}

// Provider 返回驱动名。
func (s *fakeSender) Provider() string { return "fake" }

// Send 记录消息。
func (s *fakeSender) Send(_ context.Context, msg pkgsms.Message) (pkgsms.Receipt, error) {
	s.calls++
	if s.err != nil {
		return pkgsms.Receipt{}, s.err
	}
	s.msg = msg
	return pkgsms.Receipt{Provider: s.Provider()}, nil
}

// Close 释放资源。
func (s *fakeSender) Close() error { return nil }

func TestNewCodeSenderValidation(t *testing.T) {
	if _, err := NewCodeSender(nil, "tpl", "sign", "zh-CN"); err == nil {
		t.Fatal("nil sender must be rejected")
	}
	if _, err := NewCodeSender(&fakeSender{}, " ", "sign", "zh-CN"); err == nil {
		t.Fatal("blank template id must be rejected")
	}
}

func TestCodeSenderSendCode(t *testing.T) {
	sender := &fakeSender{}
	codeSender, err := NewCodeSender(sender, "tpl-1", "sign-1", "zh-CN")
	if err != nil {
		t.Fatalf("new code sender: %v", err)
	}
	if err := codeSender.SendCode(context.Background(), "+8613800138000", "123456"); err != nil {
		t.Fatalf("send code: %v", err)
	}
	msg := sender.msg
	if msg.Phone != "+8613800138000" || msg.TemplateID != "tpl-1" || msg.SignName != "sign-1" ||
		msg.Locale != "zh-CN" || msg.Reference != "auth_code" {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if msg.Params["code"] != "123456" {
		t.Fatalf("params = %v", msg.Params)
	}
}

// TestCodeSenderBreakerAndBalanceAlert 验证供应商故障触发熔断并上报余额不足告警。
func TestCodeSenderBreakerAndBalanceAlert(t *testing.T) {
	var alerts []Alert
	sender := &fakeSender{err: pkgsms.NewProviderError(
		"aliyun", "isv.AMOUNT_NOT_ENOUGH", 0, pkgerrors.Unavailable("balance"))}
	breaker := circuitbreaker.MustNew(circuitbreaker.Config{
		Timeout:      time.Minute,
		ReadyToTrip:  func(c circuitbreaker.Counts) bool { return c.ConsecutiveFailures >= 1 },
		IsSuccessful: BreakerIsSuccessful,
	})
	codeSender, err := NewCodeSender(sender, "tpl-1", "", "",
		WithBreaker(breaker),
		WithAlertSink(func(alert Alert) { alerts = append(alerts, alert) }),
	)
	if err != nil {
		t.Fatalf("new code sender: %v", err)
	}

	if err := codeSender.SendCode(context.Background(), "+8613800138000", "123456"); err == nil {
		t.Fatal("provider failure must propagate")
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v, want one insufficient_balance", alerts)
	}
	if alerts[0].Kind != AlertKindInsufficientBalance || alerts[0].Provider != "aliyun" ||
		alerts[0].Code != "isv.AMOUNT_NOT_ENOUGH" {
		t.Fatalf("unexpected alert: %+v", alerts[0])
	}
	if breaker.State() != circuitbreaker.StateOpen {
		t.Fatalf("breaker state = %s, want open", breaker.State())
	}

	// 熔断打开后不再触网。
	before := sender.calls
	if err := codeSender.SendCode(context.Background(), "+8613800138000", "123456"); err == nil {
		t.Fatal("open circuit must return error")
	}
	if sender.calls != before {
		t.Fatalf("open circuit must not call provider: %d -> %d", before, sender.calls)
	}
}

// TestCodeSenderPermanentRejectionDoesNotTripBreaker 验证永久拒绝不计入熔断。
func TestCodeSenderPermanentRejectionDoesNotTripBreaker(t *testing.T) {
	sender := &fakeSender{err: pkgsms.Rejected("template rejected")}
	breaker := circuitbreaker.MustNew(circuitbreaker.Config{
		Timeout:      time.Minute,
		ReadyToTrip:  func(c circuitbreaker.Counts) bool { return c.ConsecutiveFailures >= 1 },
		IsSuccessful: BreakerIsSuccessful,
	})
	codeSender, err := NewCodeSender(sender, "tpl-1", "", "", WithBreaker(breaker))
	if err != nil {
		t.Fatalf("new code sender: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := codeSender.SendCode(context.Background(), "+8613800138000", "123456"); err == nil {
			t.Fatalf("attempt %d: rejection must propagate", i+1)
		}
	}
	if breaker.State() != circuitbreaker.StateClosed {
		t.Fatalf("breaker state = %s, want closed", breaker.State())
	}
	if sender.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (circuit must stay closed)", sender.calls)
	}
}

func TestCodeSenderPropagatesSendError(t *testing.T) {
	sender := &fakeSender{err: errors.New("provider down")}
	codeSender, err := NewCodeSender(sender, "tpl-1", "", "")
	if err != nil {
		t.Fatalf("new code sender: %v", err)
	}
	if err := codeSender.SendCode(context.Background(), "+8613800138000", "123456"); err == nil {
		t.Fatal("sender error must propagate")
	}
	if err := codeSender.SendCode(context.Background(), "13800138000", "123456"); err == nil {
		t.Fatal("invalid phone must be rejected")
	}
}
