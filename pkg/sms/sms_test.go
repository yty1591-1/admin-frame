package sms_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	"github.com/admin-frame/admin-frame/pkg/sms"
	"github.com/admin-frame/admin-frame/pkg/sms/smstest"
)

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sms.Message)
		want   error
	}{
		{name: "valid template", mutate: func(*sms.Message) {}},
		{name: "valid content", mutate: func(m *sms.Message) {
			m.TemplateID = ""
			m.Params = nil
			m.Content = "您的验证码是 1234"
		}},
		{name: "missing phone", mutate: func(m *sms.Message) { m.Phone = "" }, want: sms.ErrInvalidRecipient},
		{name: "phone without plus", mutate: func(m *sms.Message) { m.Phone = "8613800138000" }, want: sms.ErrInvalidRecipient},
		{name: "phone with separators", mutate: func(m *sms.Message) { m.Phone = "+86 138 0013 8000" }, want: sms.ErrInvalidRecipient},
		{name: "both template and content", mutate: func(m *sms.Message) { m.Content = "x" }, want: sms.ErrInvalidMessage},
		{name: "neither template nor content", mutate: func(m *sms.Message) { m.TemplateID = ""; m.Params = nil }, want: sms.ErrInvalidMessage},
		{name: "params without template", mutate: func(m *sms.Message) { m.TemplateID = ""; m.Content = "x" }, want: sms.ErrInvalidMessage},
		{name: "too many params", mutate: func(m *sms.Message) {
			m.Params = make(map[string]string, 21)
			for i := 0; i < 21; i++ {
				m.Params[string(rune('a'+i))] = "1"
			}
		}, want: sms.ErrInvalidMessage},
		{name: "param value too long", mutate: func(m *sms.Message) {
			m.Params = map[string]string{"code": strings.Repeat("1", 101)}
		}, want: sms.ErrInvalidMessage},
		{name: "param control char", mutate: func(m *sms.Message) {
			m.Params = map[string]string{"code": "1\n2"}
		}, want: sms.ErrInvalidMessage},
		{name: "param key invalid", mutate: func(m *sms.Message) {
			m.Params = map[string]string{"bad key": "1"}
		}, want: sms.ErrInvalidMessage},
		{name: "content too long", mutate: func(m *sms.Message) {
			m.TemplateID = ""
			m.Params = nil
			m.Content = strings.Repeat("测", 501)
		}, want: sms.ErrInvalidMessage},
		{name: "content control char", mutate: func(m *sms.Message) {
			m.TemplateID = ""
			m.Params = nil
			m.Content = "a\r\nb"
		}, want: sms.ErrInvalidMessage},
		{name: "sign name too long", mutate: func(m *sms.Message) {
			m.SignName = strings.Repeat("验", 33)
		}, want: sms.ErrInvalidMessage},
		{name: "locale invalid", mutate: func(m *sms.Message) { m.Locale = "zh_CN" }, want: sms.ErrInvalidMessage},
		{name: "idempotency key too long", mutate: func(m *sms.Message) {
			m.IdempotencyKey = strings.Repeat("k", 129)
		}, want: sms.ErrInvalidMessage},
		{name: "reference control char", mutate: func(m *sms.Message) { m.Reference = "a\nb" }, want: sms.ErrInvalidMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := smstest.ValidMessage()
			tt.mutate(&msg)
			err := msg.Validate()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
		})
	}
}

func TestMessageStringDoesNotLeakPhone(t *testing.T) {
	msg := smstest.ValidMessage()
	if strings.Contains(msg.String(), "13800138000") {
		t.Fatalf("String leaks phone: %s", msg.String())
	}
}

func TestMemoryContract(t *testing.T) {
	smstest.Run(t, func(_ *testing.T) sms.Sender {
		return sms.NewMemory(sms.MemoryConfig{Clock: clock.NewManual(time.Unix(0, 0))})
	})
}

func TestMemoryRecordsAndBounds(t *testing.T) {
	mem := sms.NewMemory(sms.MemoryConfig{MaxMessages: 2})
	t.Cleanup(func() { _ = mem.Close() })
	for i := 0; i < 3; i++ {
		msg := smstest.ValidMessage()
		msg.Reference = string(rune('a' + i))
		if _, err := mem.Send(context.Background(), msg); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	if got := mem.Len(); got != 2 {
		t.Fatalf("len=%d want 2", got)
	}
	got := mem.Messages()
	if got[0].Reference != "b" || got[1].Reference != "c" {
		t.Fatalf("unexpected retained messages: %+v", got)
	}
	got[0].Params["code"] = "mutated"
	if mem.Messages()[0].Params["code"] == "mutated" {
		t.Fatal("Messages must return a deep copy")
	}
	mem.Reset()
	if mem.Len() != 0 {
		t.Fatalf("len=%d want 0", mem.Len())
	}
}

func TestMemoryRawContentPolicy(t *testing.T) {
	mem := sms.NewMemory(sms.MemoryConfig{})
	t.Cleanup(func() { _ = mem.Close() })
	msg := smstest.ValidMessage()
	msg.TemplateID = ""
	msg.Params = nil
	msg.Content = "hello"
	if _, err := mem.Send(context.Background(), msg); !errors.Is(err, sms.ErrRejected) {
		t.Fatalf("err=%v want ErrRejected", err)
	}
	allowed := sms.NewMemory(sms.MemoryConfig{AllowRawContent: true})
	t.Cleanup(func() { _ = allowed.Close() })
	if _, err := allowed.Send(context.Background(), msg); err != nil {
		t.Fatalf("send raw: %v", err)
	}
}

func TestMemoryBatch(t *testing.T) {
	mem := sms.NewMemory(sms.MemoryConfig{})
	t.Cleanup(func() { _ = mem.Close() })
	batch, ok := sms.AsBatchSender(mem)
	if !ok {
		t.Fatal("memory should implement BatchSender")
	}
	invalid := smstest.ValidMessage()
	invalid.Phone = "bad"
	result, err := batch.SendBatch(context.Background(), []sms.Message{smstest.ValidMessage(), invalid})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if result.Failed() != 1 {
		t.Fatalf("failed=%d want 1", result.Failed())
	}
	if result.Receipts[0].MessageID == "" {
		t.Fatal("first receipt should be filled")
	}
	if !errors.Is(result.Errors[1], sms.ErrInvalidRecipient) {
		t.Fatalf("err=%v want ErrInvalidRecipient", result.Errors[1])
	}
}

func TestOpenUnknownDriver(t *testing.T) {
	_, err := sms.Open(context.Background(), sms.Config{Driver: "nope"})
	if !errors.Is(err, sms.ErrUnknownDriver) {
		t.Fatalf("err=%v want ErrUnknownDriver", err)
	}
}

func TestOpenNormalizesAndRejectsBadConfig(t *testing.T) {
	_, err := sms.Open(context.Background(), sms.Config{})
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
	_, err = sms.Open(context.Background(), sms.Config{Driver: "memory", Timeout: time.Hour})
	if !errors.Is(err, sms.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}

func TestObserverReceivesMaskedEvent(t *testing.T) {
	obs := &recordingObserver{}
	mem := sms.NewMemory(sms.MemoryConfig{Observer: obs})
	t.Cleanup(func() { _ = mem.Close() })
	if _, err := mem.Send(context.Background(), smstest.ValidMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(obs.events) != 1 {
		t.Fatalf("events=%d want 1", len(obs.events))
	}
	ev := obs.events[0]
	if ev.Channel != "sms" || ev.Result != "success" || ev.Template != "SMS_000001" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Duration < 0 {
		t.Fatalf("negative duration: %s", ev.Duration)
	}
}

func TestObserverRecordsFailureCode(t *testing.T) {
	obs := &recordingObserver{}
	mem := sms.NewMemory(sms.MemoryConfig{Observer: obs})
	t.Cleanup(func() { _ = mem.Close() })
	msg := smstest.ValidMessage()
	msg.Phone = "bad"
	if _, err := mem.Send(context.Background(), msg); err == nil {
		t.Fatal("expected error")
	}
	if len(obs.events) != 1 || obs.events[0].Result != "failure" || obs.events[0].Code != "invalid_argument" {
		t.Fatalf("unexpected events: %+v", obs.events)
	}
}

func TestDriversIncludesMemory(t *testing.T) {
	found := false
	for _, name := range sms.Drivers() {
		if name == "memory" {
			found = true
		}
	}
	if !found {
		t.Fatalf("memory driver missing: %v", sms.Drivers())
	}
}

type recordingObserver struct {
	events []sms.Event
}

func (o *recordingObserver) Observe(_ context.Context, ev sms.Event) {
	o.events = append(o.events, ev)
}
