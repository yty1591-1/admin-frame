package push_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	"github.com/admin-frame/admin-frame/pkg/push"
	"github.com/admin-frame/admin-frame/pkg/push/pushtest"
)

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*push.Message)
		want   error
	}{
		{name: "valid", mutate: func(*push.Message) {}},
		{name: "android valid", mutate: func(m *push.Message) { m.Platform = push.PlatformAndroid }},
		{name: "web valid", mutate: func(m *push.Message) { m.Platform = push.PlatformWeb }},
		{name: "missing token", mutate: func(m *push.Message) { m.Token = "" }, want: push.ErrInvalidRecipient},
		{name: "short token", mutate: func(m *push.Message) { m.Token = "abc" }, want: push.ErrInvalidRecipient},
		{name: "token with space", mutate: func(m *push.Message) { m.Token = strings.Repeat("a", 31) + " " }, want: push.ErrInvalidRecipient},
		{name: "invalid platform", mutate: func(m *push.Message) { m.Platform = "symbian" }, want: push.ErrInvalidMessage},
		{name: "missing content", mutate: func(m *push.Message) { m.Title = ""; m.Body = "" }, want: push.ErrInvalidMessage},
		{name: "title control", mutate: func(m *push.Message) { m.Title = "a\nb" }, want: push.ErrInvalidMessage},
		{name: "title too long", mutate: func(m *push.Message) { m.Title = strings.Repeat("标", 201) }, want: push.ErrInvalidMessage},
		{name: "body too long", mutate: func(m *push.Message) { m.Body = strings.Repeat("内", 1001) }, want: push.ErrInvalidMessage},
		{name: "reserved data key", mutate: func(m *push.Message) { m.Data = map[string]string{"aps": "x"} }, want: push.ErrInvalidMessage},
		{name: "bad data key", mutate: func(m *push.Message) { m.Data = map[string]string{"bad key": "x"} }, want: push.ErrInvalidMessage},
		{name: "data value control", mutate: func(m *push.Message) { m.Data = map[string]string{"k": "a\r\nb"} }, want: push.ErrInvalidMessage},
		{name: "negative badge", mutate: func(m *push.Message) { badge := -1; m.Badge = &badge }, want: push.ErrInvalidMessage},
		{name: "sound too long", mutate: func(m *push.Message) { m.Sound = strings.Repeat("s", 65) }, want: push.ErrInvalidMessage},
		{name: "bad collapse key", mutate: func(m *push.Message) { m.CollapseKey = "a b" }, want: push.ErrInvalidMessage},
		{name: "ttl too long", mutate: func(m *push.Message) { m.TTL = 8 * 24 * time.Hour }, want: push.ErrInvalidMessage},
		{name: "bad priority", mutate: func(m *push.Message) { m.Priority = "urgent" }, want: push.ErrInvalidMessage},
		{name: "payload too large", mutate: func(m *push.Message) {
			m.Data = make(map[string]string, 5)
			for i := 0; i < 5; i++ {
				m.Data[string(rune('a'+i))] = strings.Repeat("x", 1000)
			}
		}, want: push.ErrInvalidMessage},
		{name: "idempotency too long", mutate: func(m *push.Message) { m.IdempotencyKey = strings.Repeat("k", 129) }, want: push.ErrInvalidMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := pushtest.ValidMessage()
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

func TestMarshalPayload(t *testing.T) {
	msg := pushtest.ValidMessage()
	data, err := push.MarshalPayload(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	aps, ok := payload["aps"].(map[string]any)
	if !ok {
		t.Fatalf("aps missing: %s", data)
	}
	alert, ok := aps["alert"].(map[string]any)
	if !ok || alert["title"] != msg.Title {
		t.Fatalf("alert=%v", aps["alert"])
	}
	if payload["order_id"] != "123" {
		t.Fatalf("custom data missing: %s", data)
	}
	if strings.Contains(string(data), msg.Token) {
		t.Fatal("payload must not contain token")
	}
}

func TestMemoryContract(t *testing.T) {
	pushtest.Run(t, func(_ *testing.T) push.Sender {
		return push.NewMemory(push.MemoryConfig{Clock: clock.NewManual(time.Unix(0, 0))})
	})
}

func TestMemoryStoresDeepCopy(t *testing.T) {
	mem := push.NewMemory(push.MemoryConfig{MaxMessages: 1})
	t.Cleanup(func() { _ = mem.Close() })
	first := pushtest.ValidMessage()
	if _, err := mem.Send(context.Background(), first); err != nil {
		t.Fatalf("send: %v", err)
	}
	second := pushtest.ValidMessage()
	second.Reference = "second"
	if _, err := mem.Send(context.Background(), second); err != nil {
		t.Fatalf("send: %v", err)
	}
	if mem.Len() != 1 {
		t.Fatalf("len=%d want 1", mem.Len())
	}
	stored := mem.Messages()
	if stored[0].Reference != "second" {
		t.Fatalf("unexpected retained message: %+v", stored[0])
	}
	stored[0].Data["order_id"] = "mutated"
	*stored[0].Badge = 9
	if mem.Messages()[0].Data["order_id"] == "mutated" || *mem.Messages()[0].Badge == 9 {
		t.Fatal("Messages must return a deep copy")
	}
}

func TestMemoryObserverReceivesFailure(t *testing.T) {
	obs := &recordingObserver{}
	mem := push.NewMemory(push.MemoryConfig{Observer: obs})
	t.Cleanup(func() { _ = mem.Close() })
	msg := pushtest.ValidMessage()
	msg.Token = "bad"
	if _, err := mem.Send(context.Background(), msg); err == nil {
		t.Fatal("expected error")
	}
	if len(obs.events) != 1 {
		t.Fatalf("events=%d want 1", len(obs.events))
	}
	ev := obs.events[0]
	if ev.Channel != "push" || ev.Result != "failure" || ev.Platform != "ios" || ev.Code != "invalid_argument" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if strings.Contains(ev.Code, msg.Token) {
		t.Fatal("event leaks token")
	}
}

func TestOpenUnknownDriver(t *testing.T) {
	_, err := push.Open(context.Background(), push.Config{Driver: "nope"})
	if !errors.Is(err, push.ErrUnknownDriver) {
		t.Fatalf("err=%v want ErrUnknownDriver", err)
	}
}

func TestDriversIncludesMemory(t *testing.T) {
	found := false
	for _, name := range push.Drivers() {
		if name == "memory" {
			found = true
		}
	}
	if !found {
		t.Fatalf("memory driver missing: %v", push.Drivers())
	}
}

func TestMessageStringDoesNotLeakToken(t *testing.T) {
	msg := pushtest.ValidMessage()
	if strings.Contains(msg.String(), msg.Token) {
		t.Fatalf("String leaks token: %s", msg.String())
	}
}

type recordingObserver struct {
	events []push.Event
}

func (o *recordingObserver) Observe(_ context.Context, ev push.Event) {
	o.events = append(o.events, ev)
}
