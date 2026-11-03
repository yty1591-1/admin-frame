package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// memberFixture 是一次用例所需的成员 / 配额服务与已开通租户。
type memberFixture struct {
	tenants *app.Service
	members *app.MemberService
	quotas  *app.QuotaService
	emitter *recordEmitter
	tenant  domain.Tenant
}

// newMemberFixture 建立 active 租户并返回三组用例对象。
func newMemberFixture(t *testing.T) *memberFixture {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	emitter := &recordEmitter{}
	ids := &seqIDs{}

	tenants, err := app.NewService(repo, ids, emitter, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	members, err := app.NewMemberService(repo, repo, emitter, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewMemberService() error = %v", err)
	}
	quotas, err := app.NewQuotaService(repo, repo, emitter, app.Options{Service: "admin-api"})
	if err != nil {
		t.Fatalf("NewQuotaService() error = %v", err)
	}

	ctx := context.Background()
	tenant, err := tenants.Create(ctx, app.CreateInput{Name: "Acme", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tenant, err = tenants.Transition(ctx, app.TransitionInput{
		ID: tenant.ID, Target: domain.StatusActive, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("activate error = %v", err)
	}
	return &memberFixture{tenants: tenants, members: members, quotas: quotas, emitter: emitter, tenant: tenant}
}

// TestAddMemberAndDuplicate 验证添加成员成功、重复添加冲突、停用后可复活。
func TestAddMemberAndDuplicate(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()

	member, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u-owner", Role: domain.MemberRoleOwner, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if member.Status != domain.MemberStatusActive || member.Role != domain.MemberRoleOwner {
		t.Fatalf("member = %+v", member)
	}
	last, _ := fixture.emitter.last()
	if last.Action != app.ActionMemberAdd || last.ResourceType != app.ResourceTypeMember {
		t.Fatalf("审计 = %s/%s", last.Action, last.ResourceType)
	}

	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u-owner", Role: domain.MemberRoleMember, Actor: adminActor(),
	}); !isConflict(err) {
		t.Fatalf("重复添加 error = %v, want conflict", err)
	}

	// 停用后可重新添加：视为复活，角色按入参更新，审计标记 reactivated。
	// 先补一名 owner，否则停用唯一 owner 会被「最后一名 owner」规则拦住。
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u-owner2", Role: domain.MemberRoleOwner, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add(second owner) error = %v", err)
	}
	if _, err := fixture.members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: fixture.tenant.ID, SubjectID: "u-owner", Status: domain.MemberStatusDisabled,
		Reason: "离职", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("SetStatus(disabled) error = %v", err)
	}
	reactivated, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u-owner", Role: domain.MemberRoleMember, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Add(reactivate) error = %v", err)
	}
	if !reactivated.Active() || reactivated.Role != domain.MemberRoleMember {
		t.Fatalf("复活后 = %+v", reactivated)
	}
	last, _ = fixture.emitter.last()
	if last.Attrs["reactivated"] != "true" {
		t.Fatalf("reactivated = %q, want true", last.Attrs["reactivated"])
	}
}

// TestLastOwnerIsProtected 验证最后一名 owner 不能被降级或停用。
func TestLastOwnerIsProtected(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleOwner, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add(owner) error = %v", err)
	}

	_, err := fixture.members.SetRole(ctx, app.SetMemberRoleInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleAdmin, Actor: adminActor(),
	})
	if !isConflict(err) {
		t.Fatalf("降级唯一 owner error = %v, want conflict", err)
	}
	_, err = fixture.members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Status: domain.MemberStatusDisabled, Actor: adminActor(),
	})
	if !isConflict(err) {
		t.Fatalf("停用唯一 owner error = %v, want conflict", err)
	}

	// 补第二名 owner 后即可降级。
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u2", Role: domain.MemberRoleOwner, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add(second owner) error = %v", err)
	}
	if _, err := fixture.members.SetRole(ctx, app.SetMemberRoleInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleAdmin, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("降级 owner（有两名） error = %v", err)
	}
}

// TestMemberListHidesDisabledByDefault 验证默认列表不含已停用成员。
func TestMemberListHidesDisabledByDefault(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	for _, subject := range []string{"u1", "u2"} {
		if _, err := fixture.members.Add(ctx, app.AddMemberInput{
			TenantID: fixture.tenant.ID, SubjectID: subject, Role: domain.MemberRoleMember, Actor: adminActor(),
		}); err != nil {
			t.Fatalf("Add(%s) error = %v", subject, err)
		}
	}
	if _, err := fixture.members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: fixture.tenant.ID, SubjectID: "u2", Status: domain.MemberStatusDisabled, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("SetStatus() error = %v", err)
	}

	active, err := fixture.members.List(ctx, fixture.tenant.ID, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(active) != 1 || active[0].SubjectID != "u1" {
		t.Fatalf("在册成员 = %+v", active)
	}
	all, err := fixture.members.List(ctx, fixture.tenant.ID, true)
	if err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("全部成员 = %d, want 2", len(all))
	}
}

// TestMemberOperationsRejectedOnTerminatedTenant 验证终态租户不接受成员变更。
func TestMemberOperationsRejectedOnTerminatedTenant(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleOwner, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := fixture.tenants.Transition(ctx, app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusTerminated, Reason: "退租", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("terminate error = %v", err)
	}
	_, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u2", Role: domain.MemberRoleMember, Actor: adminActor(),
	})
	if !isConflict(err) {
		t.Fatalf("终态添加成员 error = %v, want conflict", err)
	}
}

// TestQuotaSetAndList 验证配额设置、不限量语义与非法资源键。
func TestQuotaSetAndList(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()

	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: 50, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	unlimited, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "api.rps", Limit: domain.QuotaUnlimited, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Set(unlimited) error = %v", err)
	}
	if !unlimited.Unlimited() {
		t.Fatalf("Limit = %d, want unlimited", unlimited.Limit)
	}

	last, _ := fixture.emitter.last()
	if last.Action != app.ActionQuotaChange || last.Attrs["limit"] != "unlimited" {
		t.Fatalf("审计 = %s limit=%s", last.Action, last.Attrs["limit"])
	}

	quotas, err := fixture.quotas.List(ctx, fixture.tenant.ID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(quotas) != 2 || quotas[0].Resource != "api.rps" || quotas[1].Resource != "user" {
		t.Fatalf("配额列表 = %+v", quotas)
	}

	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "Bad Key", Limit: 1, Actor: adminActor(),
	}); !isBadRequest(err) {
		t.Fatalf("非法资源键 error = %v, want 400", err)
	}
	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: -5, Actor: adminActor(),
	}); !isBadRequest(err) && err == nil {
		t.Fatalf("非法上限 error = %v, want error", err)
	}
}

// isConflict 判断是否为 409。
func isConflict(err error) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == apperr.CodeConflict
}

// isBadRequest 判断是否为 400。
func isBadRequest(err error) bool {
	var appErr *apperr.AppError
	return errors.As(err, &appErr) && appErr.Code == apperr.CodeBadRequest
}

// 确保 audit 包被引用（口径断言依赖 Outcome 常量）。
var _ = audit.OutcomeSuccess
