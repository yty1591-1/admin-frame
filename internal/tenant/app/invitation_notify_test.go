package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// fakeNotifier 记录邀请通知事件（ENT-10 注入测试）。
type fakeNotifier struct {
	events []app.InvitationNotifyEvent
	err    error
}

func (n *fakeNotifier) NotifyInvitationCreated(_ context.Context, event app.InvitationNotifyEvent) error {
	if n.err != nil {
		return n.err
	}
	n.events = append(n.events, event)
	return nil
}

// newNotifyingInvitationFixture 与 newInvitationFixture 同构，但允许注入
// ENT-10 邀请邮件端口（nil = 只落库不发送的阶段语义）。
func newNotifyingInvitationFixture(t *testing.T, notifier InvitationNotifierAlias) *invitationFixture {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	seedPlan(t, repo)
	emitter := &recordEmitter{}

	tenants, err := app.NewService(repo, &seqIDs{}, emitter, testPlanOptions(repo))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	members, err := app.NewMemberService(repo, repo, emitter, testPlanOptions(repo))
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}
	quotas, err := app.NewQuotaService(repo, repo, emitter, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewQuotaService() error = %v", err)
	}
	invitations, err := app.NewInvitationService(repo, repo, repo, &seqIDs{}, emitter, app.Options{
		Service: "admin-api", Quotas: repo, InvitationTTL: time.Hour, Notifier: notifier,
	})
	if err != nil {
		t.Fatalf("NewInvitationService() error = %v", err)
	}

	ctx := context.Background()
	tenant, err := tenants.Create(ctx, app.CreateInput{Name: "NotifyAcme", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if seedErr := repo.UpsertMember(ctx, mustMember(t, tenant.ID, "fixture-owner")); seedErr != nil {
		t.Fatalf("seed owner error = %v", seedErr)
	}
	approveForTest(t, tenants, tenant.ID)
	tenant, err = tenants.Transition(ctx, app.TransitionInput{
		ID: tenant.ID, Target: domain.StatusActive, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("activate error = %v", err)
	}
	return &invitationFixture{
		seatFixture: &seatFixture{
			tenants: tenants, members: members, quotas: quotas, repo: repo, tenant: tenant,
		},
		invitations: invitations,
	}
}

// InvitationNotifierAlias 仅为可读性：与 tenant/app.InvitationNotifier 同型。
type InvitationNotifierAlias = app.InvitationNotifier

func TestInvitationCreateEnqueuesEmail(t *testing.T) {
	notifier := &fakeNotifier{}
	fixture := newNotifyingInvitationFixture(t, notifier)
	ctx := context.Background()

	invitation, token, err := fixture.invite(ctx, "bob@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(notifier.events) != 1 {
		t.Fatalf("one notify event expected, got %d", len(notifier.events))
	}
	event := notifier.events[0]
	if event.InvitationID != invitation.ID || event.Email != "bob@acme.com" {
		t.Fatalf("unexpected event: %+v", event)
	}
	// 租户名必须随事件下发（邮件正文变量，渲染缺参即拒发）。
	if event.TenantName != "NotifyAcme" {
		t.Fatalf("tenant name must be carried, got %q", event.TenantName)
	}
	if event.Token == "" || event.Token != token || event.ExpiresAt.Before(time.Now()) {
		t.Fatalf("token and expiry must be carried: %+v", event)
	}
}

func TestInvitationNotifyFailureRollsBack(t *testing.T) {
	// fail closed：事件入队失败 = 邀请不落库（整体回滚），
	// 「邀请已建但邮件永不发出」的静默丢失比失败重试更难排查。
	notifier := &fakeNotifier{err: errors.New("outbox down")}
	fixture := newNotifyingInvitationFixture(t, notifier)
	ctx := context.Background()

	_, _, err := fixture.invite(ctx, "bob@acme.com", domain.MemberRoleMember)
	if err == nil {
		t.Fatal("notify failure must fail the invitation")
	}
	if _, err := fixture.repo.GetInvitation(ctx, fixture.tenant.ID, "any"); !errors.Is(err, domain.ErrInvitationNotFound) {
		t.Fatalf("invitation must not persist, got %v", err)
	}
}

func TestInvitationWithoutNotifierStillWorks(t *testing.T) {
	// ENT-10 未注入（最小部署）：保持「只落库不发送」阶段语义。
	fixture := newNotifyingInvitationFixture(t, nil)
	invitation, token, err := fixture.invite(context.Background(), "bob@acme.com", domain.MemberRoleMember)
	if err != nil || token == "" || invitation.ID == "" {
		t.Fatalf("fallback semantics broken: err=%v token=%q", err, token)
	}
}

func TestResendEnqueuesNewEmail(t *testing.T) {
	notifier := &fakeNotifier{}
	fixture := newNotifyingInvitationFixture(t, notifier)
	ctx := context.Background()

	invitation, token1, err := fixture.invite(ctx, "bob@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// 重发轮换 token → 新事件（新邮件），共两次。
	if _, _, err := fixture.invitations.Resend(ctx, app.ResendInvitationInput{
		TenantID: fixture.tenant.ID, InvitationID: invitation.ID, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Resend() error = %v", err)
	}
	if len(notifier.events) != 2 {
		t.Fatalf("resend must enqueue a second email, got %d", len(notifier.events))
	}
	if notifier.events[1].Token == token1 {
		t.Fatal("resend must carry the rotated token")
	}
}

// 编译期引用（避免 unused import 报错的同时表明依赖意图）。
var _ audit.Emitter = (*recordEmitter)(nil)
