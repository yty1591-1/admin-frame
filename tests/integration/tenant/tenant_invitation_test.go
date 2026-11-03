package tenant_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// newInvitationMySQLEnv 在真实库上构造租户 + 邀请用例（含配额仓储）。
//
// 租户保持 pending 状态：邀请与接受不要求 active（开通前拉人是合法路径）。
func newInvitationMySQLEnv(t *testing.T) (*persistence.MySQLRepository, string, *app.InvitationService) {
	t.Helper()
	sqlDB := testDB(t)
	ctx := context.Background()
	repo, err := persistence.NewMySQLRepository(sqlDB)
	if err != nil {
		t.Fatalf("NewMySQLRepository() error = %v", err)
	}
	opts := app.Options{Service: "admin-api", Plans: repo, Members: repo, Quotas: repo}
	service, err := app.NewService(repo, &uniqIDs{prefix: uniqueID(t)}, quietEmitter{}, opts)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	created, err := service.Create(ctx, app.CreateInput{
		Name: "邀请集成", OwnerSubjectID: "it-owner", Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec("DELETE FROM tenant_invitation WHERE tenant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM tenant_member WHERE tenant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM tenant_quota WHERE tenant_id = ?", created.ID)
		_, _ = sqlDB.Exec("DELETE FROM tenant WHERE id = ?", created.ID)
	})
	invitations, err := app.NewInvitationService(repo, repo, repo, &uniqIDs{prefix: uniqueID(t)},
		quietEmitter{}, app.Options{Service: "admin-api", Quotas: repo, InvitationTTL: time.Hour})
	if err != nil {
		t.Fatalf("NewInvitationService() error = %v", err)
	}
	return repo, created.ID, invitations
}

// TestInvitationFlowOnMySQL 验证邀请全流程在真实库上成立（T-14）：
// 创建（摘要落库）→ 接受（成员落库）→ 重复接受幂等 → 已接受后可再邀请。
func TestInvitationFlowOnMySQL(t *testing.T) {
	repo, tenantID, invitations := newInvitationMySQLEnv(t)
	ctx := context.Background()

	invitation, token, err := invitations.Create(ctx, app.CreateInvitationInput{
		TenantID: tenantID, Email: "it-invite@acme.com",
		Role: domain.MemberRoleMember, Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if token == "" {
		t.Fatal("应返回明文 token")
	}
	// 库里只存摘要：直接读表验证。
	var storedHash string
	if err := testDB(t).QueryRowContext(ctx,
		`SELECT token_hash FROM tenant_invitation WHERE id = ?`, invitation.ID).Scan(&storedHash); err != nil {
		t.Fatalf("scan token_hash error = %v", err)
	}
	if storedHash != domain.HashInviteToken(token) {
		t.Fatal("库里应为明文的 SHA-256 摘要")
	}

	action, err := invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "it-invitee", Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if action.Member.SubjectID != "it-invitee" || !action.Member.Active() {
		t.Fatalf("member = %+v", action.Member)
	}
	// 成员确实落库。
	if _, err := repo.GetMember(ctx, tenantID, "it-invitee"); err != nil {
		t.Fatalf("GetMember() error = %v", err)
	}

	// 重复接受幂等。
	repeat, err := invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "it-invitee", Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("重复接受 error = %v, want 幂等成功", err)
	}
	if !repeat.Idempotent {
		t.Fatal("重复接受应标记 Idempotent")
	}

	// 已接受后同邮箱可再邀请（历史行不占 pending 唯一约束——生成列 NULL 不去重）。
	invitation2, _, err := invitations.Create(ctx, app.CreateInvitationInput{
		TenantID: tenantID, Email: "it-invite@acme.com",
		Role: domain.MemberRoleAdmin, Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("已接受后再邀请 error = %v", err)
	}
	if invitation2.Status != domain.InvitationPending {
		t.Fatalf("新邀请状态 = %s", invitation2.Status)
	}
}

// TestInvitationPendingUniqueOnMySQL 验证真实库上 pending 唯一约束与过期惰性回写。
func TestInvitationPendingUniqueOnMySQL(t *testing.T) {
	repo, tenantID, invitations := newInvitationMySQLEnv(t)
	ctx := context.Background()

	invitation, token, err := invitations.Create(ctx, app.CreateInvitationInput{
		TenantID: tenantID, Email: "unique@acme.com",
		Role: domain.MemberRoleMember, Actor: itActor(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, _, err = invitations.Create(ctx, app.CreateInvitationInput{
		TenantID: tenantID, Email: "unique@acme.com",
		Role: domain.MemberRoleAdmin, Actor: itActor(),
	})
	if !errors.Is(err, domain.ErrInvitationPendingExists) {
		t.Fatalf("重复 pending error = %v, want ErrInvitationPendingExists", err)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeConflict {
		t.Fatalf("重复 pending 应映射 409, got %v", err)
	}

	// 过期：直接 UPDATE 过期时间到过去，接受 → 409 + 状态惰性回写 expired。
	if _, err := testDB(t).ExecContext(ctx,
		`UPDATE tenant_invitation SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Minute), invitation.ID); err != nil {
		t.Fatalf("回拨过期时间 error = %v", err)
	}
	if _, err := invitations.Accept(ctx, app.AcceptInvitationInput{
		Token: token, SubjectID: "it-late", Actor: itActor(),
	}); !errors.Is(err, domain.ErrInvitationExpired) {
		t.Fatalf("过期接受 error = %v, want ErrInvitationExpired", err)
	}
	reloaded, err := repo.GetInvitation(ctx, tenantID, invitation.ID)
	if err != nil {
		t.Fatalf("GetInvitation() error = %v", err)
	}
	if reloaded.Status != domain.InvitationExpired {
		t.Fatalf("Status = %s, want expired（惰性回写）", reloaded.Status)
	}
}
