package app_test

import (
	"context"
	"errors"
	"sync"
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
	repo    *persistence.MemoryRepository
	tenant  domain.Tenant
}

// newMemberFixture 建立 active 租户并返回三组用例对象。
func newMemberFixture(t *testing.T) *memberFixture {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	seedPlan(t, repo)
	emitter := &recordEmitter{}
	ids := &seqIDs{}

	tenants, err := app.NewService(repo, ids, emitter, testPlanOptions(repo))
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
	createdID := tenant.ID
	// T-12 开通前置：先种子 owner 再激活（不走 AddMember，避免审计噪声）。
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
	return &memberFixture{tenants: tenants, members: members, quotas: quotas, emitter: emitter, repo: repo, tenant: tenant}
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
	// 复活必须沿用首次加入时间：UPSERT 不覆盖 created_at，返回体不能自说自话。
	stored, err := fixture.repo.GetMember(ctx, fixture.tenant.ID, "u-owner")
	if err != nil {
		t.Fatalf("GetMember() error = %v", err)
	}
	if !reactivated.CreatedAt.Equal(stored.CreatedAt) {
		t.Fatalf("复活返回 created_at = %v，库中 = %v", reactivated.CreatedAt, stored.CreatedAt)
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
	// fixture 自带种子 owner（激活前置），先停用它使 u1 成为唯一 owner。
	if _, err := fixture.members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: fixture.tenant.ID, SubjectID: "fixture-owner", Status: domain.MemberStatusDisabled, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("disable fixture owner error = %v", err)
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
	// fixture-owner（种子）保持活跃；u2 停用后默认列表应只剩 owner 与 u1。
	if _, err := fixture.members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: fixture.tenant.ID, SubjectID: "u2", Status: domain.MemberStatusDisabled, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("SetStatus() error = %v", err)
	}

	active, _, err := fixture.members.List(ctx, fixture.tenant.ID, false, 0, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(active) != 2 || active[0].SubjectID != "fixture-owner" || active[1].SubjectID != "u1" {
		t.Fatalf("在册成员 = %+v", active)
	}
	all, _, err := fixture.members.List(ctx, fixture.tenant.ID, true, 0, 0)
	if err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("全部成员 = %d, want 3", len(all))
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

// TestUpdateRejectsInvalidStatusWithoutSideEffect 验证非法状态不会产生半提交。
//
// 旧实现先改角色再改状态，第二步报错时角色已经落库；本用例是该缺陷的回归闸门。
func TestUpdateRejectsInvalidStatusWithoutSideEffect(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleMember, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	before := len(fixture.emitter.actions())

	role := domain.MemberRoleAdmin
	bogus := domain.MemberStatus("bogus")
	_, err := fixture.members.Update(ctx, app.UpdateMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: &role, Status: &bogus, Actor: adminActor(),
	})
	if !isBadRequest(err) {
		t.Fatalf("非法状态 error = %v, want 400", err)
	}
	stored, err := fixture.repo.GetMember(ctx, fixture.tenant.ID, "u1")
	if err != nil {
		t.Fatalf("GetMember() error = %v", err)
	}
	if stored.Role != domain.MemberRoleMember {
		t.Fatalf("非法状态不应改角色，实际 role = %s", stored.Role)
	}
	if after := len(fixture.emitter.actions()); after != before {
		t.Fatalf("失败请求不应写审计，新增 %d 条", after-before)
	}
}

// TestUpdateAppliesRoleAndStatusInOneWrite 验证一次请求同时改角色与状态。
func TestUpdateAppliesRoleAndStatusInOneWrite(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleMember, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	role := domain.MemberRoleAdmin
	status := domain.MemberStatusDisabled
	updated, err := fixture.members.Update(ctx, app.UpdateMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: &role, Status: &status,
		Reason: "转岗", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Role != role || updated.Status != status {
		t.Fatalf("变更后 = %+v", updated)
	}
	actions := fixture.emitter.actions()
	if len(actions) < 2 ||
		actions[len(actions)-2] != app.ActionMemberRoleChange ||
		actions[len(actions)-1] != app.ActionMemberDisable {
		t.Fatalf("审计动作 = %v，want [role_change disable]", actions)
	}
}

// TestUpdateWithoutChangesIsIdempotent 验证无实际变化时不写库、不写审计。
func TestUpdateWithoutChangesIsIdempotent(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleMember, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	before := len(fixture.emitter.actions())

	role := domain.MemberRoleMember
	status := domain.MemberStatusActive
	if _, err := fixture.members.Update(ctx, app.UpdateMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: &role, Status: &status, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if after := len(fixture.emitter.actions()); after != before {
		t.Fatalf("空变更不应写审计，新增 %d 条", after-before)
	}
}

// TestReadsAllowedOnTerminatedTenant 验证终止态仍可读成员与配额。
//
// 只读接口套写守卫会让同一租户的成员列表 200、配额列表 409；
// 终止态恰恰是取证与对账最需要读的场景。
func TestReadsAllowedOnTerminatedTenant(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	if _, err := fixture.members.Add(ctx, app.AddMemberInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Role: domain.MemberRoleOwner, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: 10, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("SetQuota() error = %v", err)
	}
	if _, err := fixture.tenants.Transition(ctx, app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusTerminated, Reason: "退租", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("terminate error = %v", err)
	}

	if _, _, err := fixture.members.List(ctx, fixture.tenant.ID, false, 0, 0); err != nil {
		t.Fatalf("终止态查询成员 error = %v", err)
	}
	quotas, err := fixture.quotas.List(ctx, fixture.tenant.ID)
	if err != nil {
		t.Fatalf("终止态查询配额 error = %v", err)
	}
	// 物化的 api.rps（套餐默认）+ 用例设置的 user。
	if len(quotas) != 2 || quotas[1].Resource != "user" || quotas[1].Limit != 10 {
		t.Fatalf("终止态配额 = %+v", quotas)
	}
	// 写路径仍必须拒绝。
	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: 20, Actor: adminActor(),
	}); !isConflict(err) {
		t.Fatalf("终止态写配额 error = %v, want 409", err)
	}
}

// TestConcurrentOwnerDemotionKeepsOneOwner 验证并发降级不会把 owner 清零。
//
// 计数与写入若不在同一临界区，两个请求都会读到 count=2 并通过校验，
// 最终租户失去全部 owner。本用例是「至少一名 owner」不变量的并发闸门。
func TestConcurrentOwnerDemotionKeepsOneOwner(t *testing.T) {
	fixture := newMemberFixture(t)
	ctx := context.Background()
	for _, subject := range []string{"u1", "u2"} {
		if _, err := fixture.members.Add(ctx, app.AddMemberInput{
			TenantID: fixture.tenant.ID, SubjectID: subject,
			Role: domain.MemberRoleOwner, Actor: adminActor(),
		}); err != nil {
			t.Fatalf("Add(%s) error = %v", subject, err)
		}
	}
	// fixture 种子 owner 停用，保证参与并发的只有 u1 / u2。
	if _, err := fixture.members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: fixture.tenant.ID, SubjectID: "fixture-owner", Status: domain.MemberStatusDisabled, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("disable fixture owner error = %v", err)
	}

	subjects := []string{"u1", "u2"}
	errs := make([]error, len(subjects))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, subject := range subjects {
		wg.Add(1)
		go func(idx int, sid string) {
			defer wg.Done()
			<-start
			role := domain.MemberRoleAdmin
			_, errs[idx] = fixture.members.Update(ctx, app.UpdateMemberInput{
				TenantID: fixture.tenant.ID, SubjectID: sid,
				Role: &role, Actor: adminActor(),
			})
		}(i, subject)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		if !isConflict(err) {
			t.Fatalf("并发降级 error = %v, want 409", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("成功降级 %d 次，want 1（两次都成功会清空 owner）", succeeded)
	}
	members, _, err := fixture.members.List(ctx, fixture.tenant.ID, false, 0, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	owners := 0
	for _, member := range members {
		if member.Role == domain.MemberRoleOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("在册 owner = %d, want 1", owners)
	}
}
