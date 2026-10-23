package sms

import (
	"context"
	"errors"
	"testing"

	pkgsms "github.com/admin-frame/admin-frame/pkg/sms"
)

// fakeSender 记录最后一条消息并可注入错误。
type fakeSender struct {
	msg pkgsms.Message
	err error
}

// Provider 返回驱动名。
func (s *fakeSender) Provider() string { return "fake" }

// Send 记录消息。
func (s *fakeSender) Send(_ context.Context, msg pkgsms.Message) (pkgsms.Receipt, error) {
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
