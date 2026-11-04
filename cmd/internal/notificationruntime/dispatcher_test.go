package notificationruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/app"
	"github.com/admin-frame/admin-frame/internal/platform"
	tenantapp "github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// recorderStore 捕获 Dispatch 追加的 Outbox 事件（outbox.Store 降级实现）。
type recorderStore struct {
	events []*outbox.Event
}

func (r *recorderStore) Append(_ context.Context, e *outbox.Event) error {
	r.events = append(r.events, e)
	return nil
}

func (r *recorderStore) ClaimBatch(context.Context, time.Time, time.Duration, int) ([]*outbox.Event, error) {
	return nil, nil
}

func (r *recorderStore) MarkSucceeded(context.Context, string, time.Time) error { return nil }

func (r *recorderStore) Reschedule(context.Context, string, int, time.Time, time.Time, string) error {
	return nil
}

func (r *recorderStore) MarkDead(context.Context, string, time.Time, string) error   { return nil }
func (r *recorderStore) MarkFailed(context.Context, string, time.Time, string) error { return nil }

func (r *recorderStore) ReclaimStale(context.Context, time.Time, time.Time) (int, error) {
	return 0, nil
}

func (r *recorderStore) Stats(context.Context) (outbox.Stats, error) { return outbox.Stats{}, nil }

func TestInvitationNotifierPayloadContract(t *testing.T) {
	store := &recorderStore{}
	dispatcher, err := app.NewOutboxDispatcher(store)
	if err != nil {
		t.Fatalf("NewOutboxDispatcher: %v", err)
	}
	notifier := NewInvitationNotifier(dispatcher, "https://biz.example.com/")

	expires := time.Now().Add(7 * 24 * time.Hour)
	err = notifier.NotifyInvitationCreated(context.Background(), tenantapp.InvitationNotifyEvent{
		TenantID:     "t-1",
		TenantName:   "Acme",
		InvitationID: "inv-1",
		Email:        "bob@example.com",
		Role:         "member",
		Token:        "one-time-token-xyz",
		ExpiresAt:    expires,
	})
	if err != nil {
		t.Fatalf("NotifyInvitationCreated: %v", err)
	}
	if len(store.events) != 1 {
		t.Fatalf("one event expected, got %d", len(store.events))
	}
	event := store.events[0]
	var payload app.RequestPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	// 场景 / 分类 / 渠道契约：邀请是事务类邮件，偏好层不可退订。
	if payload.Scene != "tenant.invitation" || payload.SceneClass != "transactional" {
		t.Fatalf("unexpected scene: %+v", payload)
	}
	if len(payload.Channels) != 1 || payload.Channels[0] != "email" {
		t.Fatalf("unexpected channels: %v", payload.Channels)
	}
	if payload.TemplateKey != "tenant.invitation" {
		t.Fatalf("unexpected template key: %s", payload.TemplateKey)
	}
	// 渲染变量与接受链接。
	if payload.Params["tenant_name"] != "Acme" ||
		payload.Params["invitee"] != "bob@example.com" ||
		payload.Params["invite_url"] != "https://biz.example.com/invite/one-time-token-xyz" {
		t.Fatalf("unexpected params: %+v", payload.Params)
	}
	// 收件人引用是摘要（不落明文地址）。
	if payload.RecipientRef == "bob@example.com" || len(payload.RecipientRef) != 32 {
		t.Fatalf("recipient ref must be a 32-char digest, got %q", payload.RecipientRef)
	}
	// 幂等键含 token 摘要：重发轮换 token 即新键。
	if !strings.HasPrefix(payload.IdempotencyKey, "notify:tenant.invitation:inv-1:") {
		t.Fatalf("unexpected idempotency key: %s", payload.IdempotencyKey)
	}
	if payload.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expiry must follow invitation expiry")
	}
	if payload.Recipient.Email != "bob@example.com" {
		t.Fatalf("recipient address must be carried in payload only")
	}
}

func TestInvitationNotifierGuards(t *testing.T) {
	// 空 Store 构造必须失败（禁止静默丢弃）。
	if _, err := NewDispatcher(&platform.Deps{}); err == nil {
		t.Fatal("dispatcher without async must be rejected")
	}
	notifier := NewInvitationNotifier(nil, "")
	if err := notifier.NotifyInvitationCreated(context.Background(), tenantapp.InvitationNotifyEvent{
		TenantID: "t-1", InvitationID: "inv-1", Email: " ", Token: "t",
	}); err == nil {
		t.Fatal("empty email must be rejected")
	}
	// baseURL 兜底：显式传入优先，尾斜杠去除。
	store := &recorderStore{}
	dispatcher, err := app.NewOutboxDispatcher(store)
	if err != nil {
		t.Fatalf("NewOutboxDispatcher: %v", err)
	}
	n := NewInvitationNotifier(dispatcher, "https://x.example.com///")
	if err := n.NotifyInvitationCreated(context.Background(), tenantapp.InvitationNotifyEvent{
		TenantID: "t-1", TenantName: "T", InvitationID: "inv-2",
		Email: "a@b.c", Token: "tok", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	var payload app.RequestPayload
	_ = json.Unmarshal(store.events[0].Payload, &payload)
	if !strings.HasPrefix(payload.Params["invite_url"], "https://x.example.com/invite/") {
		t.Fatalf("base url must be trimmed, got %s", payload.Params["invite_url"])
	}
}
