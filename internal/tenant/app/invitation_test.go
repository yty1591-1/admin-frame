package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// invitationFixture 在 seatFixture 之上追加邀请用例对象。
type invitationFixture struct {
	*seatFixture
	invitations *app.InvitationService
}

// newInvitationFixture 建立 active 租户（种子 1 名 owner）与邀请用例。
func newInvitationFixture(t *testing.T) *invitationFixture {
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
	// 短 TTL 便于过期用例；真实默认值（7 天）由装配层验证。
	invitations, err := app.NewInvitationService(repo, repo, repo, &seqIDs{}, emitter, app.Options{
		Service: "admin-api", Quotas: repo, InvitationTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewInvitationService() error = %v", err)
	}

	ctx := context.Background()
	tenant, err := tenants.Create(ctx, app.CreateInput{Name: "Invited", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	createdID := tenant.ID
	if seedErr := repo.UpsertMember(ctx, mustMember(t, createdID, "fixture-owner")); seedErr != nil {
		t.Fatalf("seed owner error = %v", seedErr)
	}
	approveForTest(t, tenants, createdID)
	tenant, err = tenants.Transition(ctx, app.TransitionInput{
		ID: createdID, Target: domain.StatusActive, Actor: adminActor(),
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

// invite 创建一条 pending 邀请并返回（invitation, token）。
func (f *invitationFixture) invite(ctx context.Context, email string, role domain.MemberRole) (domain.Invitation, string, error) {
	return f.invitations.Create(ctx, app.CreateInvitationInput{
		TenantID: f.tenant.ID, Email: email, Role: role, Actor: adminActor(),
	})
}

// TestInvitationLifecycle 验证创建 → 接受 → 成员建立的全流程与审计。
func TestInvitationLifecycle(t *testing.T) {
	fixture := newInvitationFixture(t)
	ctx := context.Background()

	invitation, token, err := fixture.invite(ctx, "new-hire@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if token == "" || len(token) < 32 {
		t.Fatalf("明文 token 应为高熵字符串, got %q", token)
	}
	if invitation.Status != domain.InvitationPending {
		t.Fatalf("Status = %s, want pending", invitation.Status)
	}
	// 库里只存摘要：明文不得出现。
	stored, err := fixture.repo.GetInvitation(ctx, fixture.tenant.ID, invitation.ID)
	if err != nil {
		t.Fatalf("GetInvitation() error = %v", err)
	}
	if stored.TokenHash == token || strings.Contains(stored.TokenHash, token) {
		t.Fatal("库里不得出现明文 token")
	}
	if stored.TokenHash != domain.HashInviteToken(token) {
		t.Fatal("TokenHash 应为明文的 SHA-256 摘要")
	}

	action, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "u-new-hire", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if action.Idempotent {
		t.Fatal("首次接受不应标记幂等")
	}
	if action.Member.Role != domain.MemberRoleMember || !action.Member.Active() {
		t.Fatalf("member = %+v", action.Member)
	}
	if action.Invitation.Status != domain.InvitationAccepted || action.Invitation.AcceptedBy != "u-new-hire" {
		t.Fatalf("invitation = %+v", action.Invitation)
	}
	// 成员确实入库。
	if _, err := fixture.repo.GetMember(ctx, fixture.tenant.ID, "u-new-hire"); err != nil {
		t.Fatalf("GetMember() error = %v", err)
	}
}

// TestInvitationAcceptIdempotent 验证重复接受幂等：同一主体 200，换人 409。
func TestInvitationAcceptIdempotent(t *testing.T) {
	fixture := newInvitationFixture(t)
	ctx := context.Background()

	_, token, err := fixture.invite(ctx, "dup@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	first, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "u1", Actor: adminActor(),
	})
	if err != nil || first.Idempotent {
		t.Fatalf("首次接受 error = %v idempotent = %v", err, first.Idempotent)
	}

	// 同一主体重复接受 → 幂等成功，成员关系不变。
	second, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "u1", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("重复接受 error = %v, want 幂等成功", err)
	}
	if !second.Idempotent {
		t.Fatal("重复接受应标记 Idempotent")
	}
	if second.Member.SubjectID != first.Member.SubjectID {
		t.Fatalf("成员关系应一致: %s vs %s", second.Member.SubjectID, first.Member.SubjectID)
	}

	// 换人抢受 → 409。
	_, token2, err := fixture.invite(ctx, "other@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token2, SubjectID: "u1", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Accept(u1) error = %v", err)
	}
	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token2, SubjectID: "u2", Actor: adminActor(),
	}); !isConflict(err) {
		t.Fatalf("换人抢受 error = %v, want 409", err)
	}
}

// TestInvitationExpired 验证过期邀请不可用，且状态被惰性回写 expired。
func TestInvitationExpired(t *testing.T) {
	fixture := newInvitationFixture(t)
	ctx := context.Background()

	invitation, token, err := fixture.invite(ctx, "late@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// 直接把过期时间拨到过去（等价于时间流逝，避免用例等待）。
	stored, err := fixture.repo.GetInvitation(ctx, fixture.tenant.ID, invitation.ID)
	if err != nil {
		t.Fatalf("GetInvitation() error = %v", err)
	}
	stored.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	if err := fixture.repo.RefreshInvitation(ctx, stored); err != nil {
		t.Fatalf("回拨过期时间 error = %v", err)
	}

	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "u1", Actor: adminActor(),
	}); !errors.Is(err, domain.ErrInvitationExpired) {
		t.Fatalf("过期接受 error = %v, want ErrInvitationExpired", err)
	}
	reloaded, err := fixture.repo.GetInvitation(ctx, fixture.tenant.ID, invitation.ID)
	if err != nil {
		t.Fatalf("GetInvitation() error = %v", err)
	}
	if reloaded.Status != domain.InvitationExpired {
		t.Fatalf("Status = %s, want expired（惰性回写）", reloaded.Status)
	}
}

// TestInvitationRevokeAndResend 验证撤销与重发：旧 token 全部失效。
func TestInvitationRevokeAndResend(t *testing.T) {
	fixture := newInvitationFixture(t)
	ctx := context.Background()

	invitation, oldToken, err := fixture.invite(ctx, "flow@acme.com", domain.MemberRoleAdmin)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 重发：新 token 可用，旧 token 立即失效（摘要被覆盖）。
	resend, newToken, err := fixture.invitations.Resend(ctx, app.ResendInvitationInput{
		TenantID: fixture.tenant.ID, InvitationID: invitation.ID, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Resend() error = %v", err)
	}
	if resend.TokenHash == domain.HashInviteToken(oldToken) {
		t.Fatal("重发应覆盖摘要")
	}
	if newToken == "" {
		t.Fatal("重发应返回新明文 token")
	}
	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: oldToken, SubjectID: "u1", Actor: adminActor(),
	}); err == nil || !isBadRequest(err) {
		t.Fatalf("旧 token 接受 error = %v, want 400", err)
	}
	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: newToken, SubjectID: "u1", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("新 token 接受 error = %v", err)
	}

	// 撤销：pending 才可撤销，终态重复撤销 409。
	revokedInvitation, revToken, err := fixture.invite(ctx, "bye@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create(bye) error = %v", err)
	}
	revoked, err := fixture.invitations.Revoke(ctx, app.RevokeInvitationInput{
		TenantID: fixture.tenant.ID, InvitationID: revokedInvitation.ID,
		Reason: "候选人放弃", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if revoked.Status != domain.InvitationRevoked {
		t.Fatalf("Status = %s, want revoked", revoked.Status)
	}
	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: revToken, SubjectID: "u1", Actor: adminActor(),
	}); !isConflict(err) {
		t.Fatalf("已撤销 token 接受 error = %v, want 409", err)
	}
	if _, err := fixture.invitations.Revoke(ctx, app.RevokeInvitationInput{
		TenantID: fixture.tenant.ID, InvitationID: revokedInvitation.ID, Actor: adminActor(),
	}); !isConflict(err) {
		t.Fatalf("重复撤销 error = %v, want 409", err)
	}
}

// TestInvitationPendingUnique 验证同租户同邮箱仅一个 pending 邀请。
func TestInvitationPendingUnique(t *testing.T) {
	fixture := newInvitationFixture(t)
	ctx := context.Background()

	if _, _, err := fixture.invite(ctx, "solo@acme.com", domain.MemberRoleMember); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, _, err := fixture.invite(ctx, "solo@acme.com", domain.MemberRoleAdmin)
	if !errors.Is(err, domain.ErrInvitationPendingExists) {
		t.Fatalf("重复 pending error = %v, want ErrInvitationPendingExists", err)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeConflict {
		t.Fatalf("重复 pending 应映射 409, got %v", err)
	}

	// 已接受后同邮箱可再次邀请（历史行不占约束）。
	pending, _, err := fixture.invite(ctx, "back@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: mustToken(t, fixture, pending.ID), SubjectID: "u1", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if _, _, err := fixture.invite(ctx, "back@acme.com", domain.MemberRoleMember); err != nil {
		t.Fatalf("接受后再邀请 error = %v", err)
	}
}

// mustToken 从仓储摘要反查不到明文——测试需要 token，因此重新创建并返回。
// 这里通过创建函数保留 token（pending 唯一约束下不可重建），改为直接查库校验。
func mustToken(t *testing.T, f *invitationFixture, invitationID string) string {
	t.Helper()
	// 用例创建时未保留明文 → 用 Resend 换取新明文（幂等于测试目的）。
	_, token, err := f.invitations.Resend(context.Background(), app.ResendInvitationInput{
		TenantID: f.tenant.ID, InvitationID: invitationID, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Resend() error = %v", err)
	}
	return token
}

// TestInvitationSeatLimit 验证接受邀请同样受席位约束（T-13 联动）。
func TestInvitationSeatLimit(t *testing.T) {
	fixture := newInvitationFixture(t)
	ctx := context.Background()
	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: 1, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Set(user=1) error = %v", err)
	}

	_, token, err := fixture.invite(ctx, "seat@acme.com", domain.MemberRoleMember)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// owner 已占 1 席：接受应被席位拦截，且邀请保持 pending（席位恢复后仍可接受）。
	if _, err := fixture.invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "u1", Actor: adminActor(),
	}); !errors.Is(err, domain.ErrSeatLimitExceeded) {
		t.Fatalf("席位满接受 error = %v, want ErrSeatLimitExceeded", err)
	}
	reloaded, err := fixture.repo.GetInvitation(ctx, fixture.tenant.ID, tokenInviteID(t, fixture, token))
	if err != nil {
		t.Fatalf("GetInvitation() error = %v", err)
	}
	if reloaded.Status != domain.InvitationPending {
		t.Fatalf("席位拒绝不应改邀请状态, got %s", reloaded.Status)
	}
}

// tokenInviteID 通过明文 token 查邀请 ID（内存仓储仅测试路径使用）。
func tokenInviteID(t *testing.T, f *invitationFixture, token string) string {
	t.Helper()
	invitation, err := f.repo.FindByTokenHash(context.Background(), domain.HashInviteToken(token))
	if err != nil {
		t.Fatalf("FindByTokenHash() error = %v", err)
	}
	return invitation.ID
}
