package domain

import (
	"errors"
	"testing"
	"time"
)

func TestTransitionMatrixSnapshot(t *testing.T) {
	// 通知单状态机白名单快照（对齐 check-tenant TN-03 模式）：
	// 改 transitions 必须同步改这里并通过评审——迁移规则是运营承诺，
	// 不允许随手加边。
	want := map[RequestStatus][]RequestStatus{
		RequestPending:    {RequestDispatched, RequestRejected},
		RequestDispatched: {RequestSent, RequestFailed, RequestRejected},
		RequestSent:       {RequestDelivered, RequestUndelivered},
	}
	got := Transitions()
	if len(got) != len(want) {
		t.Fatalf("transition matrix changed: got %d source states, want %d", len(got), len(want))
	}
	for from, targets := range want {
		gotTargets, ok := got[from]
		if !ok {
			t.Fatalf("transition matrix changed: state %q removed", from)
		}
		if len(gotTargets) != len(targets) {
			t.Fatalf("transition matrix changed for %q: got %v, want %v", from, gotTargets, targets)
		}
		set := make(map[RequestStatus]bool, len(gotTargets))
		for _, s := range gotTargets {
			set[s] = true
		}
		for _, s := range targets {
			if !set[s] {
				t.Fatalf("transition matrix changed for %q: missing %q", from, s)
			}
		}
	}
}

func TestDeliveryTransitionMatrixSnapshot(t *testing.T) {
	want := map[DeliveryStatus][]DeliveryStatus{
		DeliveryPending:  {DeliveryAccepted, DeliveryRetrying, DeliveryFailed},
		DeliveryRetrying: {DeliveryAccepted, DeliveryRetrying, DeliveryFailed},
		DeliveryAccepted: {DeliveryDelivered, DeliveryUndelivered},
	}
	got := DeliveryTransitions()
	if len(got) != len(want) {
		t.Fatalf("delivery transition matrix changed: got %d, want %d", len(got), len(want))
	}
	for from, targets := range want {
		gotTargets, ok := got[from]
		if !ok {
			t.Fatalf("delivery transition matrix changed: state %q removed", from)
		}
		if len(gotTargets) != len(targets) {
			t.Fatalf("delivery transition matrix changed for %q: got %v, want %v", from, gotTargets, targets)
		}
		set := make(map[DeliveryStatus]bool, len(gotTargets))
		for _, s := range gotTargets {
			set[s] = true
		}
		for _, s := range targets {
			if !set[s] {
				t.Fatalf("delivery transition matrix changed for %q: missing %q", from, s)
			}
		}
	}
}

func TestNotificationTransitionsRejectIllegal(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	n := mustNotification(t, now)

	// pending → sent：跳过 dispatched 非法。
	if _, err := n.Transition(RequestSent, "", now); err == nil {
		t.Fatalf("pending->sent should be rejected")
	} else if _, ok := err.(*InvalidTransitionError); !ok {
		t.Fatalf("pending->sent should return InvalidTransitionError, got %T", err)
	}
	// rejected 必须给原因。
	if _, err := n.Transition(RequestRejected, " ", now); !errors.Is(err, ErrRejectReasonRequired) {
		t.Fatalf("reject without reason: want ErrRejectReasonRequired, got %v", err)
	}
	// 合法迁移：pending → rejected。
	next, err := n.Transition(RequestRejected, "recipient opted out", now)
	if err != nil {
		t.Fatalf("pending->rejected failed: %v", err)
	}
	if next.Version != n.Version+1 || next.RejectReason != "recipient opted out" {
		t.Fatalf("unexpected transition result: %+v", next)
	}
	// 终态不可再迁移。
	if _, err := next.Transition(RequestDispatched, "", now); !errors.Is(err, ErrRequestTerminal) {
		t.Fatalf("terminal should not transition: got %v", err)
	}
}

func TestNewNotificationValidation(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)

	cases := []struct {
		name     string
		id       string
		tenant   string
		scene    string
		ref      string
		channels []Channel
		key      string
		wantErr  error
	}{
		{"ok", "n1", "t1", "tenant.invitation", "user-1", []Channel{ChannelEmail}, "tenant.invitation", nil},
		{"no id", " ", "t1", "tenant.invitation", "user-1", []Channel{ChannelEmail}, "k", nil},
		{"no tenant", "n1", " ", "tenant.invitation", "user-1", []Channel{ChannelEmail}, "k", ErrTenantIDRequired},
		{"bad scene charset", "n1", "t1", "Tenant Inv", "user-1", []Channel{ChannelEmail}, "k", nil},
		{"no recipient", "n1", "t1", "tenant.invitation", " ", []Channel{ChannelEmail}, "k", ErrRecipientRequired},
		{"no channels", "n1", "t1", "tenant.invitation", "user-1", nil, "k", ErrChannelRequired},
		{"inbox not implemented", "n1", "t1", "tenant.invitation", "user-1", []Channel{ChannelInbox}, "k", nil},
		{"no template key", "n1", "t1", "tenant.invitation", "user-1", []Channel{ChannelEmail}, " ", ErrTemplateKeyRequired},
		{"bad priority", "n1", "t1", "tenant.invitation", "user-1", []Channel{ChannelEmail}, "k", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			priority := PriorityNormal
			if tc.name == "bad priority" {
				priority = Priority("urgent")
			}
			n, err := NewNotification(tc.id, tc.tenant, tc.scene, tc.ref, "notify:test:"+tc.id, tc.channels, tc.key, "zh-CN", priority, expires, now)
			isOk := tc.name == "ok"
			softErr := tc.name == "no id" || tc.name == "bad scene charset" ||
				tc.name == "inbox not implemented" || tc.name == "bad priority"
			switch {
			case isOk:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if n.Status != RequestPending || n.Version != 1 || n.ExpiresAt != expires {
					t.Fatalf("unexpected notification: %+v", n)
				}
			case softErr:
				if err == nil {
					t.Fatalf("expected error")
				}
			default:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
			}
		})
	}
}

func TestNotificationExpired(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	n := mustNotification(t, now)
	if n.Expired(now) {
		t.Fatal("fresh request must not be expired")
	}
	if !n.Expired(now.Add(DefaultNotificationTTL + time.Minute)) {
		t.Fatal("request must expire after TTL")
	}
	zero := n
	zero.ExpiresAt = time.Time{}
	if zero.Expired(now) {
		t.Fatal("zero ExpiresAt must be treated as never expires")
	}
}

func TestRecomputeStatus(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	mk := func(status DeliveryStatus) Delivery {
		d, err := NewDelivery("d1", "r1", "t1", ChannelEmail, now)
		if err != nil {
			t.Fatalf("NewDelivery: %v", err)
		}
		d.Status = status
		return d
	}

	cases := []struct {
		name       string
		deliveries []Delivery
		want       RequestStatus
	}{
		{"no deliveries", nil, RequestPending},
		{"pending detail", []Delivery{mk(DeliveryPending)}, RequestDispatched},
		{"retrying detail", []Delivery{mk(DeliveryRetrying)}, RequestDispatched},
		{"all accepted", []Delivery{mk(DeliveryAccepted), mk(DeliveryAccepted)}, RequestSent},
		{"mixed accepted failed", []Delivery{mk(DeliveryAccepted), mk(DeliveryFailed)}, RequestDispatched},
		{"all failed", []Delivery{mk(DeliveryFailed), mk(DeliveryFailed)}, RequestFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RecomputeStatus(tc.deliveries); got != tc.want {
				t.Fatalf("want %s, got %s", tc.want, got)
			}
		})
	}
}

func TestTemplateRender(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	tpl, err := NewTemplate("tenant.invitation", ChannelEmail, "zh-CN",
		"邀请加入 {{tenant_name}}", "你好 {{invitee}}，点击链接加入：{{invite_url}}", "admin", now)
	if err != nil {
		t.Fatalf("NewTemplate: %v", err)
	}

	// 精确匹配：全部变量就位。
	out, err := tpl.Render(map[string]string{
		"tenant_name": "Acme",
		"invitee":     "bob@example.com",
		"invite_url":  "https://example.com/i/xyz",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out.Subject != "邀请加入 Acme" || out.Body != "你好 bob@example.com，点击链接加入：https://example.com/i/xyz" {
		t.Fatalf("unexpected render: %+v", out)
	}

	// 缺参拒绝。
	if _, err := tpl.Render(map[string]string{"tenant_name": "Acme"}); err == nil {
		t.Fatal("missing params must be rejected")
	}
	// 多参拒绝（拼错键必须暴露，不能静默丢弃）。
	if _, err := tpl.Render(map[string]string{
		"tenant_name": "Acme", "invitee": "x", "invite_url": "y", "extra": "z",
	}); err == nil {
		t.Fatal("unknown param must be rejected")
	}
	// 空值视为缺失。
	if _, err := tpl.Render(map[string]string{
		"tenant_name": "Acme", "invitee": " ", "invite_url": "y",
	}); err == nil {
		t.Fatal("empty param must be rejected")
	}

	// 停用模板不可渲染。
	tpl.Enabled = false
	if _, err := tpl.Render(map[string]string{"tenant_name": "a", "invitee": "b", "invite_url": "c"}); !errors.Is(err, ErrTemplateDisabled) {
		t.Fatalf("disabled template: want ErrTemplateDisabled, got %v", err)
	}
}

func TestPreferenceTransactionalOptOut(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if _, err := NewPreference("t1", SubjectUser, "u1", ClassTransactional, ChannelSms, false, now); !errors.Is(err, ErrTransactionalOptOut) {
		t.Fatalf("transactional opt-out must be rejected, got %v", err)
	}
	p, err := NewPreference("t1", SubjectUser, "u1", ClassMarketing, ChannelEmail, false, now)
	if err != nil {
		t.Fatalf("marketing opt-out failed: %v", err)
	}
	if p.Enabled {
		t.Fatal("opted-out preference must have Enabled=false")
	}
}

func TestBlacklistHMACShape(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if _, err := NewBlacklist("t1", ChannelEmail, "deadbeef", BlacklistComplaint, "spam", "admin", now); !errors.Is(err, ErrTargetHMACInvalid) {
		t.Fatalf("short hmac must be rejected, got %v", err)
	}
	if _, err := NewBlacklist("t1", ChannelEmail, "a", BlacklistComplaint, "spam", "admin", now); !errors.Is(err, ErrTargetHMACInvalid) {
		t.Fatalf("invalid source-free hmac must be rejected, got %v", err)
	}
	b, err := NewBlacklist("t1", ChannelEmail, "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789", BlacklistBounce, "hard bounce", "system", now)
	if err != nil {
		t.Fatalf("valid blacklist rejected: %v", err)
	}
	if b.TargetHMAC != "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Fatalf("hmac must be lower-cased, got %q", b.TargetHMAC)
	}
}

func mustNotification(t *testing.T, now time.Time) Notification {
	t.Helper()
	n, err := NewNotification("n1", "t1", "tenant.invitation", "user-1", "notify:tenant.invitation:inv-1",
		[]Channel{ChannelEmail, ChannelSms}, "tenant.invitation", "zh-CN", PriorityNormal,
		now.Add(time.Hour), now)
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	return n
}
