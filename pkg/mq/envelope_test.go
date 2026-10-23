package mq_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/mq"
)

func validEnvelope() mq.Envelope {
	return mq.Envelope{
		EventID:    "evt-1",
		EventType:  "demo.created",
		Version:    "v1",
		TenantID:   "t-1",
		OccurredAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		TraceID:    "trace-1",
		Payload:    []byte(`{"id":"1"}`),
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	env := validEnvelope()
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	msg := env.ToMessage("demo.topic")
	if msg.Topic != "demo.topic" || msg.ID != env.EventID {
		t.Fatalf("message = %+v", msg)
	}
	if got := mq.HeaderGet(msg.Headers, mq.HeaderEventType); got != env.EventType {
		t.Fatalf("event_type header = %q", got)
	}
	back, err := mq.EnvelopeFromMessage(msg)
	if err != nil {
		t.Fatalf("EnvelopeFromMessage: %v", err)
	}
	if back.EventID != env.EventID || back.EventType != env.EventType ||
		back.TenantID != env.TenantID || back.TraceID != env.TraceID ||
		!back.OccurredAt.Equal(env.OccurredAt) || !bytes.Equal(back.Payload, env.Payload) {
		t.Fatalf("round trip = %+v", back)
	}
}

func TestEnvelopeToMessageDefaultsTopicToEventType(t *testing.T) {
	msg := validEnvelope().ToMessage("")
	if msg.Topic != "demo.created" {
		t.Fatalf("topic = %q", msg.Topic)
	}
}

func TestEnvelopeNormalizeFillsDefaults(t *testing.T) {
	env := mq.Envelope{EventID: "evt-1", EventType: "demo.created", Payload: []byte(`{}`)}
	normalized := env.Normalize()
	if normalized.Version != mq.DefaultEventVersion || normalized.OccurredAt.IsZero() {
		t.Fatalf("normalized = %+v", normalized)
	}
	if err := normalized.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestEnvelopeValidateRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		edit func(*mq.Envelope)
	}{
		{name: "missing id", edit: func(e *mq.Envelope) { e.EventID = "" }},
		{name: "missing type", edit: func(e *mq.Envelope) { e.EventType = "" }},
		{name: "missing version", edit: func(e *mq.Envelope) { e.Version = "" }},
		{name: "missing occurred_at", edit: func(e *mq.Envelope) { e.OccurredAt = time.Time{} }},
		{name: "empty payload", edit: func(e *mq.Envelope) { e.Payload = nil }},
		{name: "array payload", edit: func(e *mq.Envelope) { e.Payload = []byte(`[1]`) }},
		{name: "multiple objects", edit: func(e *mq.Envelope) { e.Payload = []byte(`{} {}`) }},
		{name: "oversize", edit: func(e *mq.Envelope) {
			e.Payload = append([]byte(`{"k":"`), make([]byte, mq.MaxEventPayloadBytes)...)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnvelope()
			tc.edit(&env)
			if err := env.Validate(); err == nil {
				t.Fatal("expected error")
			} else if !errors.Is(err, mq.ErrInvalidMessage) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestEnvelopeFromMessageRejectsMissingHeaders(t *testing.T) {
	msg := mq.Message{Topic: "demo.topic", Body: []byte(`{}`)}
	if _, err := mq.EnvelopeFromMessage(msg); err == nil {
		t.Fatal("expected error for missing event headers")
	}
}

func TestEnvelopeIdempotencyKey(t *testing.T) {
	env := validEnvelope()
	if got := env.IdempotencyKeyOrEventID(); got != "evt-1" {
		t.Fatalf("key = %q", got)
	}
	env.IdempotencyKey = "biz-1"
	if got := env.IdempotencyKeyOrEventID(); got != "biz-1" {
		t.Fatalf("key = %q", got)
	}
	msg := env.ToMessage("demo.topic")
	if got := mq.HeaderGet(msg.Headers, mq.HeaderIdempotencyKey); got != "biz-1" {
		t.Fatalf("header key = %q", got)
	}
}
