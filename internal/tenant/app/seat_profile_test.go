package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/tenant/app"
	"github.com/admin-frame/admin-frame/internal/tenant/domain"
	"github.com/admin-frame/admin-frame/internal/tenant/infrastructure/persistence"
)

// seatFixture 是带配额仓储的成员 / 租户用例对象（T-13 席位校验需要）。
type seatFixture struct {
	tenants *app.Service
	members *app.MemberService
	quotas  *app.QuotaService
	repo    *persistence.MemoryRepository
	tenant  domain.Tenant
}

// newSeatFixture 建立 active 租户（种子 1 名 owner）并注入配额仓储。
func newSeatFixture(t *testing.T) *seatFixture {
	t.Helper()
	repo := persistence.NewMemoryRepository()
	seedPlan(t, repo)
	emitter := &recordEmitter{}

	tenants, err := app.NewService(repo, &seqIDs{}, emitter, testPlanOptions(repo))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	// 与生产装配一致：成员用例拿到配额仓储后启用席位校验。
	members, err := app.NewMemberService(repo, repo, emitter, testPlanOptions(repo))
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
	if seedErr := repo.UpsertMember(ctx, mustMember(t, createdID, "fixture-owner")); seedErr != nil {
		t.Fatalf("seed owner error = %v", seedErr)
	}
	tenant, err = tenants.Transition(ctx, app.TransitionInput{
		ID: createdID, Target: domain.StatusActive, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("activate error = %v", err)
	}
	return &seatFixture{tenants: tenants, members: members, quotas: quotas, repo: repo, tenant: tenant}
}

// addMember 添加成员的小包装。
func (f *seatFixture) addMember(ctx context.Context, subject string) error {
	_, err := f.members.Add(ctx, app.AddMemberInput{
		TenantID: f.tenant.ID, SubjectID: subject, Role: domain.MemberRoleMember, Actor: adminActor(),
	})
	return err
}

// TestSeatLimitRejectsBeyondQuota 验证超出 user 配额返回 409 并携带当前值与上限。
func TestSeatLimitRejectsBeyondQuota(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()
	// 套餐种子 user limit=50；收敛到 2 便于构造超限场景（1 名 owner 已占席）。
	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: 2, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Set(user=2) error = %v", err)
	}

	if err := fixture.addMember(ctx, "u1"); err != nil {
		t.Fatalf("Add(u1) error = %v", err)
	}
	err := fixture.addMember(ctx, "u2")
	// translate 保留原始错误链：哨兵可匹配，消息携带当前值与上限。
	if !errors.Is(err, domain.ErrSeatLimitExceeded) {
		t.Fatalf("Add(u2) error = %v, want seat limit", err)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeConflict {
		t.Fatalf("席位超限应映射为 409, got %v", err)
	}
	if !strings.Contains(appErr.Message, "active 2") || !strings.Contains(appErr.Message, "limit 2") {
		t.Fatalf("错误信息应携带当前值与上限: %q", appErr.Message)
	}

	// 停用 u1 释放席位后可再添加：上限按「在册」计数，不是按历史总数。
	if _, err := fixture.members.SetStatus(ctx, app.SetMemberStatusInput{
		TenantID: fixture.tenant.ID, SubjectID: "u1", Status: domain.MemberStatusDisabled,
		Reason: "离职", Actor: adminActor(),
	}); err != nil {
		t.Fatalf("SetStatus(u1 disabled) error = %v", err)
	}
	if err := fixture.addMember(ctx, "u3"); err != nil {
		t.Fatalf("Add(u3 after disable) error = %v", err)
	}
}

// TestSeatLimitCountsReactivation 验证复活停用成员同样受席位约束。
func TestSeatLimitCountsReactivation(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()
	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: 1, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Set(user=1) error = %v", err)
	}

	// owner 已占 1 席：先加 u1（应被拒），无法构造停用成员。
	if err := fixture.addMember(ctx, "u1"); !isConflict(err) {
		t.Fatalf("满员添加 error = %v, want conflict", err)
	}
}

// TestSeatLimitUnlimitedQuota 验证 limit=-1（不限）不拦添加。
func TestSeatLimitUnlimitedQuota(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()
	if _, err := fixture.quotas.Set(ctx, app.SetQuotaInput{
		TenantID: fixture.tenant.ID, Resource: "user", Limit: domain.QuotaUnlimited, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("Set(user=-1) error = %v", err)
	}
	for _, subject := range []string{"u1", "u2", "u3"} {
		if err := fixture.addMember(ctx, subject); err != nil {
			t.Fatalf("Add(%s) error = %v", subject, err)
		}
	}
}

// TestCreateWithExplicitCode 验证业务编码：显式编码生效、重复冲突、非法值 400。
func TestCreateWithExplicitCode(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()

	created, err := fixture.tenants.Create(ctx, app.CreateInput{
		Name: "Beta", Code: "beta-co", Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("Create(code) error = %v", err)
	}
	if created.Code != "beta-co" {
		t.Fatalf("Code = %q, want beta-co", created.Code)
	}

	// 未提供编码 → 以租户 ID 兜底（与迁移 000013 同源）。
	defaulted, err := fixture.tenants.Create(ctx, app.CreateInput{Name: "Gamma", Actor: adminActor()})
	if err != nil {
		t.Fatalf("Create(default code) error = %v", err)
	}
	if defaulted.Code != defaulted.ID {
		t.Fatalf("Code = %q, want %q", defaulted.Code, defaulted.ID)
	}

	if _, err := fixture.tenants.Create(ctx, app.CreateInput{
		Name: "Dup", Code: "beta-co", Actor: adminActor(),
	}); !isConflict(err) {
		t.Fatalf("重复编码 error = %v, want conflict", err)
	}
	if _, err := fixture.tenants.Create(ctx, app.CreateInput{
		Name: "Bad", Code: "Bad Code!", Actor: adminActor(),
	}); !isBadRequest(err) {
		t.Fatalf("非法编码 error = %v, want 400", err)
	}
}

// TestUpdateProfile 验证资料变更：改名、清空语义、空请求拒绝、编码不可变。
func TestUpdateProfile(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()

	name := "Acme Renamed"
	email := "ops@acme.com"
	tz := "Asia/Shanghai"
	updated, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: fixture.tenant.ID, Name: &name, ContactEmail: &email, Timezone: &tz, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	if updated.Name != name || updated.ContactEmail != email || updated.Timezone != tz {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.Version != fixture.tenant.Version+1 {
		t.Fatalf("Version = %d, want %d", updated.Version, fixture.tenant.Version+1)
	}

	// 显式空串 = 清空字段；nil = 不变。
	cleared := ""
	after, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: fixture.tenant.ID, ContactEmail: &cleared, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("UpdateProfile(clear) error = %v", err)
	}
	if after.ContactEmail != "" || after.Name != name || after.Timezone != tz {
		t.Fatalf("清空语义错误: %+v", after)
	}

	if _, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: fixture.tenant.ID, Actor: adminActor(),
	}); !isBadRequest(err) {
		t.Fatalf("空变更 error = %v, want 400", err)
	}

	if _, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: "no-such-tenant", Name: &name, Actor: adminActor(),
	}); err == nil {
		t.Fatal("不存在的租户应返回错误")
	} else {
		var appErr *apperr.AppError
		if !errors.As(err, &appErr) || appErr.Code != apperr.CodeNotFound {
			t.Fatalf("不存在 error = %v, want 404", err)
		}
	}

	// 非法邮箱 / 超长字段 → 400。
	bad := "not-an-email"
	if _, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: fixture.tenant.ID, ContactEmail: &bad, Actor: adminActor(),
	}); !isBadRequest(err) {
		t.Fatalf("非法邮箱 error = %v, want 400", err)
	}
	tooLong := strings.Repeat("行", domain.MaxIndustryLength+1)
	if _, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: fixture.tenant.ID, Industry: &tooLong, Actor: adminActor(),
	}); !isBadRequest(err) {
		t.Fatalf("超长行业 error = %v, want 400", err)
	}
}

// TestMemberListPagination 验证成员列表分页与总数。
func TestMemberListPagination(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()
	for _, subject := range []string{"u1", "u2", "u3"} {
		if err := fixture.addMember(ctx, subject); err != nil {
			t.Fatalf("Add(%s) error = %v", subject, err)
		}
	}

	members, total, err := fixture.members.List(ctx, fixture.tenant.ID, false, 2, 0)
	if err != nil {
		t.Fatalf("List(page1) error = %v", err)
	}
	if total != 4 || len(members) != 2 {
		t.Fatalf("page1: total=%d items=%d, want 4/2", total, len(members))
	}
	// owner 在前。
	if members[0].Role != domain.MemberRoleOwner {
		t.Fatalf("首条应为 owner, got %s", members[0].Role)
	}

	members, total, err = fixture.members.List(ctx, fixture.tenant.ID, false, 2, 2)
	if err != nil {
		t.Fatalf("List(page2) error = %v", err)
	}
	if total != 4 || len(members) != 2 {
		t.Fatalf("page2: total=%d items=%d, want 4/2", total, len(members))
	}

	// 越界页返回空但不报错。
	members, total, err = fixture.members.List(ctx, fixture.tenant.ID, false, 2, 100)
	if err != nil || total != 4 || len(members) != 0 {
		t.Fatalf("越界页: err=%v total=%d items=%d", err, total, len(members))
	}
}

// TestTransitionExpectedVersionMismatch 验证 T-15：迁移的期望版本不符返回 409 并携带当前版本。
func TestTransitionExpectedVersionMismatch(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()

	stale := int64(99)
	_, err := fixture.tenants.Transition(ctx, app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusActive,
		ExpectedVersion: &stale, Actor: adminActor(),
	})
	var mismatch *domain.VersionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want VersionMismatchError", err)
	}
	if mismatch.Expected != 99 || mismatch.Current != fixture.tenant.Version {
		t.Fatalf("mismatch = %+v, want expected 99 / current %d", mismatch, fixture.tenant.Version)
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.Code != apperr.CodeConflict {
		t.Fatalf("应映射 409, got %v", err)
	}

	// 正确版本 → 迁移成功（fixture 已 active，走 suspend）。
	current := fixture.tenant.Version
	if _, err := fixture.tenants.Transition(ctx, app.TransitionInput{
		ID: fixture.tenant.ID, Target: domain.StatusSuspended, Reason: "条件更新验证",
		ExpectedVersion: &current, Actor: adminActor(),
	}); err != nil {
		t.Fatalf("正确版本迁移 error = %v", err)
	}
}

// TestUpdateProfileExpectedVersionMismatch 验证资料变更的条件更新（T-15）。
func TestUpdateProfileExpectedVersionMismatch(t *testing.T) {
	fixture := newSeatFixture(t)
	ctx := context.Background()

	name := "X"
	stale := int64(42)
	if _, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: fixture.tenant.ID, Name: &name,
		ExpectedVersion: &stale, Actor: adminActor(),
	}); !isConflict(err) {
		t.Fatalf("过期版本更新 error = %v, want 409", err)
	}

	current := fixture.tenant.Version
	updated, err := fixture.tenants.UpdateProfile(ctx, app.UpdateProfileInput{
		ID: fixture.tenant.ID, Name: &name,
		ExpectedVersion: &current, Actor: adminActor(),
	})
	if err != nil {
		t.Fatalf("正确版本更新 error = %v", err)
	}
	if updated.Version != current+1 {
		t.Fatalf("Version = %d, want %d", updated.Version, current+1)
	}
}
