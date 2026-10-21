package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func newTestRole(t *testing.T, id, code string, mutate func(*domain.NewRoleParams)) *domain.Role {
	t.Helper()
	params := domain.NewRoleParams{
		ID:      id,
		Channel: domain.ChannelAdmin,
		Scope:   domain.RoleScopePlatform,
		Code:    code,
		Name:    "测试角色 " + code,
		Type:    domain.RoleTypeCustom,
		Actor:   "actor-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	role, err := domain.NewRole(params)
	if err != nil {
		t.Fatalf("NewRole(%s): %v", code, err)
	}
	return role
}

func TestMemoryRoleRepositoryCreateAndFind(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleRepository()
	role := newTestRole(t, "role-1", "ops_read", nil)

	if err := repo.Save(ctx, role, 0); err != nil {
		t.Fatal(err)
	}

	byID, err := repo.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	if byID.Code != role.Code {
		t.Fatalf("code=%s", byID.Code)
	}

	byCode, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", role.Code)
	if err != nil {
		t.Fatal(err)
	}
	if byCode.ID != role.ID {
		t.Fatalf("id=%s", byCode.ID)
	}

	if _, err := repo.FindByID(ctx, "missing"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
	if _, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", "missing"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

func TestMemoryRoleRepositoryUniqueness(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleRepository()
	role := newTestRole(t, "role-1", "ops_read", nil)
	if err := repo.Save(ctx, role, 0); err != nil {
		t.Fatal(err)
	}

	if err := repo.Save(ctx, newTestRole(t, "role-1", "other_code", nil), 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate id err=%v, want conflict", err)
	}
	if err := repo.Save(ctx, newTestRole(t, "role-2", "ops_read", nil), 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("duplicate code err=%v, want conflict", err)
	}

	// B 端同 code 属于不同唯一键，允许共存。
	bizRole := newTestRole(t, "role-3", "ops_read", func(p *domain.NewRoleParams) {
		p.Channel = domain.ChannelBiz
		p.Scope = domain.RoleScopeMerchant
		p.MerchantID = "m-1"
	})
	if err := repo.Save(ctx, bizRole, 0); err != nil {
		t.Fatalf("biz role with same code must be allowed: %v", err)
	}
}

func TestMemoryRoleRepositoryOptimisticLock(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleRepository()
	role := newTestRole(t, "role-1", "ops_read", nil)
	if err := repo.Save(ctx, role, 0); err != nil {
		t.Fatal(err)
	}

	if err := role.UpdateDetails("新名字", "", "actor-2", role.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, role, 0); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("create on existing id err=%v, want conflict", err)
	}
	if err := repo.Save(ctx, role, 1); err != nil {
		t.Fatalf("stale expected version must still match stored version 1: %v", err)
	}

	// 已存版本为 2，用 1 更新必须冲突。
	stale := cloneRole(role)
	stale.Name = "过期写入"
	if err := repo.Save(ctx, stale, 1); !errors.Is(err, apperr.ErrConflict) {
		t.Fatalf("stale write err=%v, want conflict", err)
	}

	missing := newTestRole(t, "role-missing", "missing_role", nil)
	if err := repo.Save(ctx, missing, 1); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("update missing err=%v, want not found", err)
	}
}

func TestMemoryRoleRepositoryRejectsImmutableChange(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleRepository()
	role := newTestRole(t, "role-1", "ops_read", nil)
	if err := repo.Save(ctx, role, 0); err != nil {
		t.Fatal(err)
	}

	tampered := cloneRole(role)
	tampered.Code = "renamed_code"
	tampered.Version = role.Version + 1
	if err := repo.Save(ctx, tampered, role.Version); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err=%v, want bad request", err)
	}
}

func TestMemoryRoleRepositoryList(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleRepository()

	roles := []*domain.Role{
		newTestRole(t, "role-1", "alpha", nil),
		newTestRole(t, "role-2", "beta", func(p *domain.NewRoleParams) { p.Name = "订单管理员" }),
		newTestRole(t, "role-3", "gamma", nil),
	}
	for _, role := range roles {
		if err := repo.Save(ctx, role, 0); err != nil {
			t.Fatal(err)
		}
	}
	disabled := cloneRole(roles[2])
	if err := disabled.Disable("actor-1", disabled.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, disabled, roles[2].Version); err != nil {
		t.Fatal(err)
	}
	deleted := cloneRole(roles[0])
	if err := deleted.Delete("actor-1", deleted.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, deleted, roles[0].Version); err != nil {
		t.Fatal(err)
	}

	all, err := repo.List(ctx, domain.RoleFilter{Channel: domain.ChannelAdmin, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("len=%d, want 2 (deleted excluded)", len(all))
	}
	if all[0].Code != "beta" || all[1].Code != "gamma" {
		t.Fatalf("order=%s,%s", all[0].Code, all[1].Code)
	}

	// 软删角色仍可按 code 查到（code 不可复用）。
	if _, err := repo.FindByCode(ctx, domain.ChannelAdmin, "", "alpha"); err != nil {
		t.Fatalf("deleted role must remain findable by code: %v", err)
	}

	active, err := repo.List(ctx, domain.RoleFilter{Status: domain.RoleStatusActive, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Code != "beta" {
		t.Fatalf("active=%d", len(active))
	}

	keyword, err := repo.List(ctx, domain.RoleFilter{Keyword: "订单", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(keyword) != 1 || keyword[0].Code != "beta" {
		t.Fatalf("keyword=%d", len(keyword))
	}

	// 租户条件：B 端商户只能看到自己的角色。
	bizRole := newTestRole(t, "role-4", "merchant_owner", func(p *domain.NewRoleParams) {
		p.Channel = domain.ChannelBiz
		p.Scope = domain.RoleScopeMerchant
		p.MerchantID = "m-1"
	})
	if err := repo.Save(ctx, bizRole, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.List(ctx, domain.RoleFilter{MerchantID: "m-1", Limit: 10}); err != nil || len(got) != 1 {
		t.Fatalf("merchant filter got=%d err=%v", len(got), err)
	}
	if got, err := repo.List(ctx, domain.RoleFilter{MerchantID: "m-2", Limit: 10}); err != nil || len(got) != 0 {
		t.Fatalf("other merchant got=%d err=%v", len(got), err)
	}

	// Count 与 List 使用同一套过滤条件，且不受分页影响。
	if got, err := repo.Count(ctx, domain.RoleFilter{Channel: domain.ChannelAdmin}); err != nil || got != 2 {
		t.Fatalf("count admin got=%d err=%v", got, err)
	}
	if got, err := repo.Count(ctx, domain.RoleFilter{MerchantID: "m-1"}); err != nil || got != 1 {
		t.Fatalf("count merchant got=%d err=%v", got, err)
	}
	if got, err := repo.Count(ctx, domain.RoleFilter{Limit: 1, Offset: 100}); err != nil || got != 3 {
		t.Fatalf("count must ignore pagination got=%d err=%v", got, err)
	}

	paged, err := repo.List(ctx, domain.RoleFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(paged) != 1 || paged[0].Code != "gamma" {
		t.Fatalf("paged=%+v", paged)
	}

	empty, err := repo.List(ctx, domain.RoleFilter{Offset: 100})
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("out-of-range must return empty non-nil slice, got %+v", empty)
	}
}

func TestMemoryRoleRepositoryIsolatesState(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleRepository()
	role := newTestRole(t, "role-1", "ops_read", nil)
	set, err := domain.NewPermissionSet([]iam.PermissionCode{iam.PermRoleRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := role.ReplacePermissions(set, "actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	scope, err := domain.NewDataScope("order", domain.DataScopeAll, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := role.ReplaceDataScopes([]domain.DataScope{scope}, "actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, role, 0); err != nil {
		t.Fatal(err)
	}

	// 修改调用方对象与返回值都不得影响仓储内部状态。
	role.Permissions[iam.PermOrderRead] = struct{}{}
	loaded, err := repo.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Permissions[iam.PermFileRead] = struct{}{}
	loaded.DataScopes["order"] = domain.DataScope{Resource: "order", Type: domain.DataScopeSelf}

	again, err := repo.FindByID(ctx, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.HasPermission(iam.PermOrderRead) || again.HasPermission(iam.PermFileRead) {
		t.Fatalf("repository state leaked: %v", again.PermissionCodes())
	}
	if got, ok := again.DataScopeFor("order"); !ok || got.Type != domain.DataScopeAll {
		t.Fatalf("scope leaked: %+v", got)
	}
}

func newTestBinding(t *testing.T, id, subjectID, roleID string, mutate func(*domain.NewBindingParams)) *domain.Binding {
	t.Helper()
	params := domain.NewBindingParams{
		ID:          id,
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   subjectID,
		RoleID:      roleID,
		GrantedBy:   "actor-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	binding, err := domain.NewBinding(params)
	if err != nil {
		t.Fatalf("NewBinding(%s): %v", id, err)
	}
	return binding
}

func TestMemoryBindingRepositorySaveIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryBindingRepository()

	first := newTestBinding(t, "binding-1", "u-1", "role-1", nil)
	if err := repo.Save(ctx, first); err != nil {
		t.Fatal(err)
	}

	// 同一唯一键、不同 ID：幂等成功且不新增记录。
	duplicate := newTestBinding(t, "binding-2", "u-1", "role-1", nil)
	if err := repo.Save(ctx, duplicate); err != nil {
		t.Fatalf("duplicate binding must be idempotent: %v", err)
	}
	if _, err := repo.FindByID(ctx, "binding-2"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("duplicate id must not be stored, err=%v", err)
	}
	if _, err := repo.FindByID(ctx, "binding-1"); err != nil {
		t.Fatalf("original binding must be kept: %v", err)
	}

	if err := repo.Save(ctx, nil); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("nil binding err=%v, want bad request", err)
	}
}

func TestMemoryBindingRepositoryDeleteIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryBindingRepository()
	binding := newTestBinding(t, "binding-1", "u-1", "role-1", nil)
	if err := repo.Save(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, binding.ID); err != nil {
		t.Fatalf("second delete must be idempotent: %v", err)
	}
	if err := repo.Delete(ctx, "missing"); err != nil {
		t.Fatalf("unknown id delete must be idempotent: %v", err)
	}
	// 解绑后同一唯一键可重新绑定（新 ID）。
	if err := repo.Save(ctx, newTestBinding(t, "binding-2", "u-1", "role-1", nil)); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryBindingRepositoryListBySubject(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryBindingRepository()
	now := time.Now().UTC()

	expiresSoon := now.Add(time.Minute)
	expired := now.Add(-time.Minute)

	bindings := []*domain.Binding{
		newTestBinding(t, "binding-1", "u-1", "role-b", nil),
		newTestBinding(t, "binding-2", "u-1", "role-a", nil),
		newTestBinding(t, "binding-3", "u-1", "role-c", func(p *domain.NewBindingParams) {
			p.ExpiresAt = &expiresSoon
		}),
		newTestBinding(t, "binding-4", "u-2", "role-a", nil),
	}
	for _, binding := range bindings {
		if err := repo.Save(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	// 直接写入已过期绑定，模拟历史数据到期。
	repo.mu.Lock()
	repo.bindings["binding-expired"] = &domain.Binding{
		ID:          "binding-expired",
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "u-1",
		RoleID:      "role-d",
		ExpiresAt:   &expired,
	}
	repo.mu.Unlock()

	got, err := repo.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "u-1",
		At:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3 (expired filtered)", len(got))
	}
	if got[0].RoleID != "role-a" || got[1].RoleID != "role-b" || got[2].RoleID != "role-c" {
		t.Fatalf("order=%s,%s,%s", got[0].RoleID, got[1].RoleID, got[2].RoleID)
	}

	// 到期后同样被过滤。
	later, err := repo.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "u-1",
		At:          now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(later) != 2 {
		t.Fatalf("len=%d, want 2 after expiry", len(later))
	}
}

func TestMemoryBindingRepositoryTenantIsolation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryBindingRepository()
	bizBinding := newTestBinding(t, "binding-1", "u-1", "role-1", func(p *domain.NewBindingParams) {
		p.Channel = domain.ChannelBiz
		p.SubjectType = domain.SubjectMerchantStaff
		p.MerchantID = "m-1"
	})
	if err := repo.Save(ctx, bizBinding); err != nil {
		t.Fatal(err)
	}

	// B 端空商户必须直接报错，而不是放宽条件返回所有商户的绑定。
	_, err := repo.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   "u-1",
	})
	if !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("err=%v, want bad request", err)
	}

	// 不同商户查不到。
	got, err := repo.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   "u-1",
		MerchantID:  "m-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("cross tenant leak: %d bindings", len(got))
	}

	got, err = repo.ListBySubject(ctx, domain.SubjectQuery{
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   "u-1",
		MerchantID:  "m-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
}

func TestMemoryBindingRepositoryQueryValidation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryBindingRepository()

	cases := []domain.SubjectQuery{
		{Channel: "both", SubjectType: domain.SubjectStaff, SubjectID: "u-1"},
		{Channel: domain.ChannelAdmin, SubjectType: "guest", SubjectID: "u-1"},
		{Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff},
		{Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "u-1", MerchantID: "m-1"},
	}
	for _, query := range cases {
		if _, err := repo.ListBySubject(ctx, query); !errors.Is(err, apperr.ErrBadRequest) {
			t.Fatalf("query %+v err=%v, want bad request", query, err)
		}
	}

	if _, err := repo.ListByRole(ctx, ""); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("empty role id err=%v, want bad request", err)
	}
}

func TestMemoryBindingRepositoryListByRole(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryBindingRepository()
	expired := time.Now().UTC().Add(-time.Minute)

	for _, binding := range []*domain.Binding{
		newTestBinding(t, "binding-1", "u-1", "role-1", nil),
		newTestBinding(t, "binding-2", "u-2", "role-1", nil),
		newTestBinding(t, "binding-3", "u-3", "role-2", nil),
	} {
		if err := repo.Save(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	// 已过期绑定仍须出现在 ListByRole 中，供删除角色前检查引用。
	repo.mu.Lock()
	repo.bindings["binding-expired"] = &domain.Binding{
		ID:      "binding-expired",
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff,
		SubjectID: "u-9", RoleID: "role-1", ExpiresAt: &expired,
	}
	repo.mu.Unlock()

	got, err := repo.ListByRole(ctx, "role-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}

	// 返回值修改不影响仓储。
	got[0].SubjectID = "tampered"
	again, err := repo.FindByID(ctx, got[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.SubjectID == "tampered" {
		t.Fatal("repository state leaked through ListByRole")
	}
}
