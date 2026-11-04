package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/notification/infrastructure/persistence"
)

func TestReplayServiceCompensatesFailedRequest(t *testing.T) {
	ctx := context.Background()
	requests := persistence.NewMemoryRequestRepository()
	store := &recorder{}
	dispatcher, err := NewOutboxDispatcher(store)
	if err != nil {
		t.Fatalf("NewOutboxDispatcher: %v", err)
	}
	dlq := persistence.NewMemoryDLQReader()
	emitter := &recordEmitter{}
	svc, err := NewReplayService(requests, dispatcher, dlq, newIDGenerator(t), emitter, Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewReplayService: %v", err)
	}

	// 造一条 failed 通知单 + 对应 DLQ 载荷。
	payload := testPayload()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	n := mustFailedRequest(t, payload)
	if err := requests.Create(ctx, n); err != nil {
		t.Fatalf("Create: %v", err)
	}
	dlq.Put(n.ID, raw)

	newID, err := svc.Replay(ctx, ReplayInput{RequestID: n.ID, Actor: testActor()})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if newID == "" || newID == n.ID {
		t.Fatalf("replay must create a new request id, got %q", newID)
	}
	if len(store.events) != 1 {
		t.Fatalf("one replay event expected, got %d", len(store.events))
	}
	event := store.events[0]
	if event.IdempotencyKey != "notify:replay:"+newID {
		t.Fatalf("replay idempotency key must derive from new id, got %s", event.IdempotencyKey)
	}
	var replayPayload RequestPayload
	if err := json.Unmarshal(event.Payload, &replayPayload); err != nil {
		t.Fatalf("decode replay payload: %v", err)
	}
	// 补偿保留原始投递信息（渲染参数与地址），换新身份重新排队。
	if replayPayload.Scene != payload.Scene || replayPayload.Recipient.Email != payload.Recipient.Email {
		t.Fatalf("replay payload must preserve delivery info: %+v", replayPayload)
	}
	if replayPayload.ReplayOf != n.ID {
		t.Fatalf("replay payload must record origin, got %q", replayPayload.ReplayOf)
	}
	if !replayPayload.ExpiresAt.After(time.Now()) {
		t.Fatalf("replay must get a fresh expiry, got %v", replayPayload.ExpiresAt)
	}
	// 补偿动作必须入审计。
	found := false
	for _, a := range emitter.actions() {
		if a == ActionDLQReplay {
			found = true
		}
	}
	if !found {
		t.Fatalf("replay must be audited, actions=%v", emitter.actions())
	}
}

func TestReplayServiceGuards(t *testing.T) {
	ctx := context.Background()
	requests := persistence.NewMemoryRequestRepository()
	dlq := persistence.NewMemoryDLQReader()
	svc, err := NewReplayService(requests, mustDispatcher(t), dlq, newIDGenerator(t), &recordEmitter{}, Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewReplayService: %v", err)
	}

	// 非 failed 状态不可重放。
	n := mustFailedRequest(t, testPayload())
	n.Status = domain.RequestSent
	n.IdempotencyKey = "notify:test:sent"
	if err := requests.Create(ctx, n); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Replay(ctx, ReplayInput{RequestID: n.ID, Actor: testActor()}); err == nil ||
		!strings.Contains(err.Error(), "only failed") {
		t.Fatalf("non-failed request must be rejected, got %v", err)
	}

	// failed 但 DLQ 无载荷（历史数据 / 事件被清理）：404 语义。
	failed := mustFailedRequest(t, testPayload())
	failed.Status = domain.RequestFailed
	failed.IdempotencyKey = "notify:test:failed"
	if err := requests.Create(ctx, failed); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Replay(ctx, ReplayInput{RequestID: failed.ID, Actor: testActor()}); err == nil {
		t.Fatal("missing dlq payload must be rejected")
	}

	// 依赖缺失必须拒绝构造。
	if _, err := NewReplayService(nil, mustDispatcher(t), dlq, newIDGenerator(t), &recordEmitter{}, Options{}); err == nil {
		t.Fatal("nil requests must be rejected")
	}
	if _, err := NewReplayService(requests, mustDispatcher(t), nil, newIDGenerator(t), &recordEmitter{}, Options{}); err == nil {
		t.Fatal("nil dlq must be rejected")
	}
}

func mustDispatcher(t *testing.T) *OutboxDispatcher {
	t.Helper()
	d, err := NewOutboxDispatcher(&recorder{})
	if err != nil {
		t.Fatalf("NewOutboxDispatcher: %v", err)
	}
	return d
}

func mustFailedRequest(t *testing.T, payload RequestPayload) domain.Notification {
	t.Helper()
	channels := make([]domain.Channel, 0, len(payload.Channels))
	for _, raw := range payload.Channels {
		channels = append(channels, domain.Channel(raw))
	}
	priority := domain.Priority(payload.Priority)
	if !priority.Valid() {
		priority = domain.PriorityNormal
	}
	n, err := domain.NewNotification(payload.RequestID, payload.TenantID, payload.Scene,
		payload.RecipientRef, payload.IdempotencyKey, channels, payload.TemplateKey,
		payload.Locale, priority, payload.ExpiresAt, time.Now())
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	n.Status = domain.RequestFailed
	return n
}
