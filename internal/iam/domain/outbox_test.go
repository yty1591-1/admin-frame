package domain

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func validOutboxParams(now time.Time) NewOutboxEventParams {
	return NewOutboxEventParams{
		ID:            "1001",
		Type:          OutboxEventAudit,
		AggregateType: "iam_role",
		AggregateID:   "2001",
		Payload:       []byte(`{"action":"rbac.role.change"}`),
		Now:           now,
	}
}

func TestNewOutboxEvent(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	event, err := NewOutboxEvent(validOutboxParams(now))
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}
	if event.Status != OutboxPending || event.Attempts != 0 || event.NextRetryAt != nil {
		t.Fatalf("unexpected initial state: %+v", event)
	}
	if !event.CreatedAt.Equal(now) || !event.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps must be UTC and equal to now: %+v", event)
	}
	if !event.IsDue(now) {
		t.Fatal("pending event without next_retry_at must be due")
	}
}

func TestNewOutboxEventRejectsBadInput(t *testing.T) {
	now := time.Now().UTC()
	cases := map[string]func(*NewOutboxEventParams){
		"missing id": func(p *NewOutboxEventParams) { p.ID = " " },
		"unsupported type": func(p *NewOutboxEventParams) {
			p.Type = OutboxEventType("unknown")
		},
		"missing aggregate type":   func(p *NewOutboxEventParams) { p.AggregateType = "" },
		"missing aggregate id":     func(p *NewOutboxEventParams) { p.AggregateID = "" },
		"empty payload":            func(p *NewOutboxEventParams) { p.Payload = nil },
		"non JSON payload":         func(p *NewOutboxEventParams) { p.Payload = []byte("not-json") },
		"JSON array payload":       func(p *NewOutboxEventParams) { p.Payload = []byte(`[1,2]`) },
		"trailing JSON document":   func(p *NewOutboxEventParams) { p.Payload = []byte(`{"a":1}{"b":2}`) },
		"payload over size budget": func(p *NewOutboxEventParams) { p.Payload = oversizedPayload() },
	}
	for name, mutate := range cases {
		params := validOutboxParams(now)
		mutate(&params)
		if _, err := NewOutboxEvent(params); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestOutboxEventStateMachine(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	event, err := NewOutboxEvent(validOutboxParams(now))
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}

	if err := event.MarkSucceeded(now); err == nil {
		t.Fatal("pending event must not be marked succeeded without claim")
	}
	if err := event.Claim(now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := event.Claim(now); err == nil {
		t.Fatal("claiming twice must conflict")
	}
	if err := event.MarkSucceeded(now); err != nil {
		t.Fatalf("MarkSucceeded: %v", err)
	}
	if event.Status != OutboxSucceeded || event.NextRetryAt != nil {
		t.Fatalf("unexpected final state: %+v", event)
	}
	if event.IsDue(now.Add(time.Hour)) {
		t.Fatal("succeeded event must not be due")
	}
}

func TestOutboxEventRescheduleBacksOffUntilDead(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	policy := OutboxRetryPolicy{Base: time.Second, Max: 8 * time.Second, MaxAttempts: 3}
	event, err := NewOutboxEvent(validOutboxParams(now))
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}
	if err := event.Claim(now); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := event.Reschedule(now, policy); err != nil {
		t.Fatalf("Reschedule #1: %v", err)
	}
	if event.Status != OutboxPending || event.Attempts != 1 || event.NextRetryAt == nil {
		t.Fatalf("after first failure: %+v", event)
	}
	if !event.NextRetryAt.Equal(now.Add(time.Second)) {
		t.Fatalf("first backoff = %s, want 1s", event.NextRetryAt.Sub(now))
	}
	if event.IsDue(now.Add(500 * time.Millisecond)) {
		t.Fatal("event must not be due before next_retry_at")
	}

	// 第二次：4s；第三次：达到上限，进入死信。
	if err := event.Claim(now.Add(2 * time.Second)); err != nil {
		t.Fatalf("Claim #2: %v", err)
	}
	if err := event.Reschedule(now.Add(2*time.Second), policy); err != nil {
		t.Fatalf("Reschedule #2: %v", err)
	}
	if event.NextRetryAt == nil || !event.NextRetryAt.Equal(now.Add(4*time.Second)) {
		t.Fatalf("second backoff = %v, want now+4s", event.NextRetryAt)
	}
	if err := event.Claim(now.Add(7 * time.Second)); err != nil {
		t.Fatalf("Claim #3: %v", err)
	}
	if err := event.Reschedule(now.Add(7*time.Second), policy); err != nil {
		t.Fatalf("Reschedule #3: %v", err)
	}
	if event.Status != OutboxDead || event.NextRetryAt != nil {
		t.Fatalf("retries exhausted: %+v", event)
	}
}

func TestOutboxRetryPolicyNormalizeAndCap(t *testing.T) {
	policy := OutboxRetryPolicy{}
	normalized := policy.Normalize()
	if normalized.Base != time.Second || normalized.Max != 5*time.Minute || normalized.MaxAttempts != 8 {
		t.Fatalf("unexpected defaults: %+v", normalized)
	}
	policy = OutboxRetryPolicy{Base: time.Second, Max: 5 * time.Second, MaxAttempts: 10}
	for attempts, want := range map[int]time.Duration{
		1: time.Second,
		2: 2 * time.Second,
		3: 4 * time.Second,
		4: 5 * time.Second,
		9: 5 * time.Second,
	} {
		if got := policy.NextRetry(attempts); got != want {
			t.Fatalf("NextRetry(%d) = %s, want %s", attempts, got, want)
		}
	}
	if policy.Exhausted(9) {
		t.Fatal("9 attempts must not exhaust a 10-attempt policy")
	}
	if !policy.Exhausted(10) {
		t.Fatal("10 attempts must exhaust a 10-attempt policy")
	}
}

func TestOutboxEventPayloadIsIsolated(t *testing.T) {
	now := time.Now().UTC()
	payload := []byte(`{"action":"x"}`)
	params := validOutboxParams(now)
	params.Payload = payload
	event, err := NewOutboxEvent(params)
	if err != nil {
		t.Fatalf("NewOutboxEvent: %v", err)
	}
	payload[0] = '['
	if bytes.Equal(event.Payload, payload) {
		t.Fatal("event payload must not alias caller slice")
	}
	if !json.Valid(event.Payload) {
		t.Fatalf("payload corrupted: %s", event.Payload)
	}
}

func oversizedPayload() []byte {
	value := make([]byte, MaxOutboxPayloadBytes+1)
	for i := range value {
		value[i] = 'a'
	}
	value[0] = '"'
	value[len(value)-1] = '"'
	return append([]byte(`{"blob":`), append(value, '}')...)
}
