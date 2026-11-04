package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// stubFeatureEvaluator 按租户集合返回生效值（FF-07 测试）；
// allowAll=true 时对任意租户放行。
type stubFeatureEvaluator struct {
	allowAll       bool
	enabledTenants map[string]bool
	evals          int
}

func (e *stubFeatureEvaluator) IsEnabled(flagKey, _, tenantID, subjectID string) bool {
	e.evals++
	if flagKey != app.FlagInvitationEmail {
		return false
	}
	if e.allowAll {
		return true
	}
	return e.enabledTenants[tenantID] && subjectID != ""
}

// TestInvitationEmailGatedByFeatureFlag ENT-11 FF-07：flag 禁用 = 邀请
// 照常落库但不发送邮件（运营线下转交兜底）；evaluator 未注入沿用
// ENT-10 语义（notifier 注入即发送）。
func TestInvitationEmailGatedByFeatureFlag(t *testing.T) {
	ctx := context.Background()

	// ① evaluator 拒绝 → 落库成功、notifier 零调用。
	denied := &stubFeatureEvaluator{enabledTenants: map[string]bool{}}
	notifier := &fakeNotifier{}
	deniedFixture := newNotifyingInvitationFixture(t, notifier)
	deniedFixture.invitations.SetFeatureEvaluator(denied)
	invitation, token, err := deniedFixture.invite(ctx, "bob@acme.com", domain.MemberRoleMember)
	if err != nil || invitation.ID == "" || token == "" {
		t.Fatalf("flag-disabled invitation must still persist: err=%v", err)
	}
	if len(notifier.events) != 0 {
		t.Fatalf("flag-disabled tenant must not enqueue email, events=%d", len(notifier.events))
	}
	if denied.evals == 0 {
		t.Fatal("evaluator must be consulted")
	}

	// ② evaluator 放行 → 正常入队。
	allowed := &stubFeatureEvaluator{allowAll: true}
	allowedNotifier := &fakeNotifier{}
	allowedFixture := newNotifyingInvitationFixture(t, allowedNotifier)
	allowedFixture.invitations.SetFeatureEvaluator(allowed)
	if _, _, err := allowedFixture.invite(ctx, "carol@acme.com", domain.MemberRoleMember); err != nil {
		t.Fatalf("flag-enabled invitation failed: %v", err)
	}
	if len(allowedNotifier.events) != 1 {
		t.Fatalf("flag-enabled tenant must enqueue email, events=%d", len(allowedNotifier.events))
	}

	// ③ evaluator 未注入 → 沿用 ENT-10 语义（notifier 注入即发送）。
	legacyNotifier := &fakeNotifier{}
	legacyFixture := newNotifyingInvitationFixture(t, legacyNotifier)
	if _, _, err := legacyFixture.invite(ctx, "dave@acme.com", domain.MemberRoleMember); err != nil {
		t.Fatalf("nil evaluator invitation failed: %v", err)
	}
	if len(legacyNotifier.events) != 1 {
		t.Fatalf("nil evaluator must keep ENT-10 semantics, events=%d", len(legacyNotifier.events))
	}
}
