package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// mustNewRole 构造一个合法的 A 端平台角色，可用 mutate 覆盖字段。
func mustNewRole(t *testing.T, mutate func(*NewRoleParams)) *Role {
	t.Helper()
	params := NewRoleParams{
		ID:      "role-1",
		Channel: ChannelAdmin,
		Scope:   RoleScopePlatform,
		Code:    "ops_readonly",
		Name:    "运维只读",
		Type:    RoleTypeCustom,
		Actor:   "actor-1",
	}
	if mutate != nil {
		mutate(&params)
	}
	role, err := NewRole(params)
	if err != nil {
		t.Fatalf("NewRole: %v", err)
	}
	return role
}

// mustNewBizRole 构造一个合法的 B 端商户角色。
func mustNewBizRole(t *testing.T, mutate func(*NewRoleParams)) *Role {
	t.Helper()
	return mustNewRole(t, func(p *NewRoleParams) {
		p.Channel = ChannelBiz
		p.Scope = RoleScopeMerchant
		p.MerchantID = "m-1"
		p.Code = "merchant_owner"
		if mutate != nil {
			mutate(p)
		}
	})
}

func TestNewRoleRejectsSelfParent(t *testing.T) {
	_, err := NewRole(NewRoleParams{
		ID:       "role-1",
		Channel:  ChannelAdmin,
		Scope:    RoleScopePlatform,
		Code:     "ops_readonly",
		Name:     "运维只读",
		Actor:    "actor-1",
		ParentID: "role-1",
	})
	if !errors.Is(err, ErrInvalidRoleParent) {
		t.Fatalf("err = %v, want ErrInvalidRoleParent", err)
	}
}

func TestChangeParent(t *testing.T) {
	role := mustNewRole(t, func(p *NewRoleParams) { p.ParentID = "role-parent" })
	if role.ParentID != "role-parent" {
		t.Fatalf("parent = %q", role.ParentID)
	}
	// 父角色未变化：幂等返回，不递增版本。
	if err := role.ChangeParent("role-parent", "actor-2", role.Version); err != nil {
		t.Fatalf("idempotent ChangeParent: %v", err)
	}
	if role.Version != 1 {
		t.Fatalf("version = %d, want 1", role.Version)
	}
	if err := role.ChangeParent(role.ID, "actor-2", role.Version); !errors.Is(err, ErrInvalidRoleParent) {
		t.Fatalf("self parent err = %v, want ErrInvalidRoleParent", err)
	}
	if err := role.ChangeParent("role-grandparent", "actor-2", role.Version); err != nil {
		t.Fatalf("ChangeParent: %v", err)
	}
	if role.ParentID != "role-grandparent" || role.Version != 2 {
		t.Fatalf("parent=%q version=%d", role.ParentID, role.Version)
	}

	breakglass := &Role{
		ID:      "role-bg",
		Channel: ChannelAdmin,
		Scope:   RoleScopePlatform,
		Type:    RoleTypeBreakglass,
		Status:  RoleStatusActive,
		Version: 1,
	}
	if err := breakglass.ChangeParent("role-parent", "actor-2", breakglass.Version); !errors.Is(err, ErrInvalidRoleParent) {
		t.Fatalf("breakglass parent err = %v, want ErrInvalidRoleParent", err)
	}
}

func TestRoleMaxBindings(t *testing.T) {
	limit := 3
	role := mustNewRole(t, func(p *NewRoleParams) { p.MaxBindings = &limit })
	if role.MaxBindings == nil || *role.MaxBindings != 3 {
		t.Fatalf("max bindings = %v", role.MaxBindings)
	}
	// 参数被复制：修改原指针不影响聚合。
	limit = 9
	if *role.MaxBindings != 3 {
		t.Fatalf("max bindings must be cloned, got %d", *role.MaxBindings)
	}

	// 相同值幂等，不递增版本。
	same := 3
	if err := role.SetMaxBindings(&same, "actor-2", role.Version); err != nil {
		t.Fatalf("idempotent SetMaxBindings: %v", err)
	}
	if role.Version != 1 || *role.MaxBindings != 3 {
		t.Fatalf("version=%d max=%d", role.Version, *role.MaxBindings)
	}

	zero := 0
	if err := role.SetMaxBindings(&zero, "actor-2", role.Version); !errors.Is(err, ErrInvalidRoleConstraint) {
		t.Fatalf("zero limit err = %v, want ErrInvalidRoleConstraint", err)
	}

	five := 5
	if err := role.SetMaxBindings(&five, "actor-2", role.Version); err != nil {
		t.Fatalf("SetMaxBindings: %v", err)
	}
	if role.Version != 2 || *role.MaxBindings != 5 {
		t.Fatalf("version=%d max=%d", role.Version, *role.MaxBindings)
	}
	if err := role.SetMaxBindings(nil, "actor-2", role.Version); err != nil {
		t.Fatalf("clear max bindings: %v", err)
	}
	if role.MaxBindings != nil || role.Version != 3 {
		t.Fatalf("max bindings=%v version=%d", role.MaxBindings, role.Version)
	}

	// 创建时同样拒绝非法上限。
	zeroAgain := 0
	_, err := NewRole(NewRoleParams{
		ID: "role-2", Channel: ChannelAdmin, Scope: RoleScopePlatform,
		Code: "ops_readonly", Name: "运维只读", Actor: "actor-1", MaxBindings: &zeroAgain,
	})
	if !errors.Is(err, ErrInvalidRoleConstraint) {
		t.Fatalf("new role err = %v, want ErrInvalidRoleConstraint", err)
	}
}

func TestNewRoleDefaults(t *testing.T) {
	role := mustNewRole(t, nil)
	if role.Status != RoleStatusActive {
		t.Fatalf("status=%s", role.Status)
	}
	if role.Version != 1 {
		t.Fatalf("version=%d", role.Version)
	}
	if role.CreatedBy != "actor-1" || role.UpdatedBy != "actor-1" {
		t.Fatalf("audit fields=%s/%s", role.CreatedBy, role.UpdatedBy)
	}
	if role.CreatedAt.IsZero() || role.UpdatedAt.IsZero() {
		t.Fatal("timestamps must be set")
	}
	if role.Permissions == nil || role.DataScopes == nil {
		t.Fatal("collections must be initialized")
	}
	if !role.IsActive() || role.IsDeleted() {
		t.Fatal("new role must be active and not deleted")
	}
}

func TestNewRoleValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*NewRoleParams)
		wantErr error
		code    int
	}{
		{name: "missing id", mutate: func(p *NewRoleParams) { p.ID = "" }, wantErr: ErrEmptyInput, code: apperr.CodeBadRequest},
		{name: "missing actor", mutate: func(p *NewRoleParams) { p.Actor = "" }, wantErr: ErrMissingActor, code: apperr.CodeBadRequest},
		{name: "bad channel", mutate: func(p *NewRoleParams) { p.Channel = "both" }, wantErr: ErrInvalidChannel, code: apperr.CodeBadRequest},
		{name: "bad scope", mutate: func(p *NewRoleParams) { p.Scope = "global" }, wantErr: ErrInvalidRoleScope, code: apperr.CodeBadRequest},
		{
			name:    "platform with merchant",
			mutate:  func(p *NewRoleParams) { p.MerchantID = "m-1" },
			wantErr: ErrInvalidRoleScope,
			code:    apperr.CodeBadRequest,
		},
		{
			name: "merchant on admin channel",
			mutate: func(p *NewRoleParams) {
				p.Scope = RoleScopeMerchant
				p.MerchantID = "m-1"
			},
			wantErr: ErrInvalidRoleScope,
			code:    apperr.CodeBadRequest,
		},
		{
			name: "merchant without merchant id",
			mutate: func(p *NewRoleParams) {
				p.Channel = ChannelBiz
				p.Scope = RoleScopeMerchant
			},
			wantErr: ErrInvalidRoleScope,
			code:    apperr.CodeBadRequest,
		},
		{name: "bad code", mutate: func(p *NewRoleParams) { p.Code = "Bad-Code" }, wantErr: ErrInvalidRoleCode, code: apperr.CodeBadRequest},
		{name: "wildcard code", mutate: func(p *NewRoleParams) { p.Code = "role*" }, wantErr: ErrWildcardNotAllowed, code: apperr.CodeBadRequest},
		{name: "empty name", mutate: func(p *NewRoleParams) { p.Name = "   " }, wantErr: ErrEmptyInput, code: apperr.CodeBadRequest},
		{
			name:    "long name",
			mutate:  func(p *NewRoleParams) { p.Name = strings.Repeat("名", MaxRoleNameLen) },
			wantErr: ErrEmptyInput,
			code:    apperr.CodeBadRequest,
		},
		{
			name:    "long description",
			mutate:  func(p *NewRoleParams) { p.Description = strings.Repeat("x", MaxRoleDescriptionLen+1) },
			wantErr: ErrEmptyInput,
			code:    apperr.CodeBadRequest,
		},
		{name: "bad type", mutate: func(p *NewRoleParams) { p.Type = "root" }, wantErr: ErrInvalidRoleScope, code: apperr.CodeBadRequest},
		{
			name:    "breakglass rejected",
			mutate:  func(p *NewRoleParams) { p.Type = RoleTypeBreakglass },
			wantErr: ErrBreakglassRequiresApproval,
			code:    apperr.CodeForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := NewRoleParams{
				ID:      "role-1",
				Channel: ChannelAdmin,
				Scope:   RoleScopePlatform,
				Code:    "ops_readonly",
				Name:    "运维只读",
				Actor:   "actor-1",
				Type:    RoleTypeCustom,
			}
			tc.mutate(&params)
			_, err := NewRole(params)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			var ae *apperr.AppError
			if !errors.As(err, &ae) || ae.Code != tc.code {
				t.Fatalf("err=%v, want code %d", err, tc.code)
			}
		})
	}
}

func TestNewBizRole(t *testing.T) {
	role := mustNewBizRole(t, nil)
	if role.Channel != ChannelBiz || role.MerchantID != "m-1" {
		t.Fatalf("role=%+v", role)
	}
}

func TestReplacePermissions(t *testing.T) {
	role := mustNewRole(t, nil)
	next, err := NewPermissionSet([]iam.PermissionCode{iam.PermRoleRead, iam.PermRoleWrite})
	if err != nil {
		t.Fatal(err)
	}

	change, err := role.ReplacePermissions(next, "actor-2", role.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Added) != 2 || len(change.Removed) != 0 {
		t.Fatalf("change=%+v", change)
	}
	if role.Version != 2 || role.UpdatedBy != "actor-2" {
		t.Fatalf("version=%d updatedBy=%s", role.Version, role.UpdatedBy)
	}
	if !role.HasPermission(iam.PermRoleRead) {
		t.Fatal("permission must be granted")
	}
	if got := role.PermissionCodes(); len(got) != 2 {
		t.Fatalf("codes=%v", got)
	}

	// 相同集合：不产生差量，也不递增版本。
	change, err = role.ReplacePermissions(next.Clone(), "actor-2", role.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !change.IsEmpty() {
		t.Fatalf("change=%+v", change)
	}
	if role.Version != 2 {
		t.Fatalf("version=%d, want 2", role.Version)
	}

	// 收缩授权：返回 removed 供审计。
	smaller, err := NewPermissionSet([]iam.PermissionCode{iam.PermOrderRead})
	if err != nil {
		t.Fatal(err)
	}
	change, err = role.ReplacePermissions(smaller, "actor-2", role.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Removed) != 2 || len(change.Added) != 1 {
		t.Fatalf("change=%+v", change)
	}
	if role.HasPermission(iam.PermRoleRead) {
		t.Fatal("removed permission must be revoked")
	}
}

func TestReplacePermissionsAllowsNilSet(t *testing.T) {
	role := mustNewRole(t, nil)
	change, err := role.ReplacePermissions(nil, "actor-1", role.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !change.IsEmpty() {
		t.Fatalf("change=%+v", change)
	}
	if role.Permissions == nil {
		t.Fatal("permissions must not become nil")
	}
}

func TestReplacePermissionsRejectsDisabledRole(t *testing.T) {
	role := mustNewRole(t, nil)
	if err := role.Disable("actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	set, err := NewPermissionSet([]iam.PermissionCode{iam.PermRoleRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := role.ReplacePermissions(set, "actor-1", role.Version); !errors.Is(err, ErrRoleNotActive) {
		t.Fatalf("err=%v, want ErrRoleNotActive", err)
	}
}

// TestVersionConflict 是 TESTING_STANDARDS 必测函数：乐观锁不匹配必须拒绝。
func TestVersionConflict(t *testing.T) {
	role := mustNewRole(t, nil)
	if err := role.UpdateDetails("新名字", "", "actor-1", role.Version+1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err=%v, want ErrVersionConflict", err)
	}
	var ae *apperr.AppError
	err := role.Disable("actor-1", 99)
	if !errors.As(err, &ae) || ae.Code != apperr.CodeConflict {
		t.Fatalf("err=%v, want conflict code", err)
	}
	// 冲突后状态不得变化。
	if role.Status != RoleStatusActive || role.Version != 1 {
		t.Fatalf("role mutated on conflict: %+v", role)
	}
}

func TestRoleMutationsRequireActor(t *testing.T) {
	role := mustNewRole(t, nil)
	if err := role.UpdateDetails("新名字", "", "", role.Version); !errors.Is(err, ErrMissingActor) {
		t.Fatalf("err=%v, want ErrMissingActor", err)
	}
}

func TestUpdateDetails(t *testing.T) {
	role := mustNewRole(t, nil)
	if err := role.UpdateDetails("  新名字  ", "说明", "actor-2", role.Version); err != nil {
		t.Fatal(err)
	}
	if role.Name != "新名字" {
		t.Fatalf("name=%q", role.Name)
	}
	if role.Description != "说明" {
		t.Fatalf("description=%q", role.Description)
	}
	if role.Version != 2 {
		t.Fatalf("version=%d", role.Version)
	}
	if err := role.UpdateDetails("", "", "actor-2", role.Version); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("err=%v, want ErrEmptyInput", err)
	}
	if err := role.UpdateDetails("名字", strings.Repeat("x", MaxRoleDescriptionLen+1), "actor-2", role.Version); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("err=%v, want ErrEmptyInput", err)
	}
}

func TestDisableEnableLifecycle(t *testing.T) {
	role := mustNewRole(t, nil)
	if err := role.Disable("actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if role.Status != RoleStatusDisabled || role.IsActive() {
		t.Fatalf("role=%+v", role)
	}
	version := role.Version
	// 重复停用幂等：不递增版本。
	if err := role.Disable("actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if role.Version != version {
		t.Fatalf("version=%d, want %d", role.Version, version)
	}

	if err := role.Enable("actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if !role.IsActive() {
		t.Fatal("role must be active again")
	}
	version = role.Version
	if err := role.Enable("actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if role.Version != version {
		t.Fatalf("version=%d, want %d", role.Version, version)
	}
}

func TestDeleteIsTerminal(t *testing.T) {
	role := mustNewRole(t, nil)
	if err := role.Delete("actor-2", role.Version); err != nil {
		t.Fatal(err)
	}
	if !role.IsDeleted() || role.DeletedAt == nil {
		t.Fatal("role must be marked deleted")
	}
	if role.Status != RoleStatusDisabled {
		t.Fatalf("status=%s", role.Status)
	}
	if role.IsActive() {
		t.Fatal("deleted role must not be active")
	}
	// 再改任何字段都必须失败。
	if err := role.UpdateDetails("名字", "", "actor-2", role.Version); !errors.Is(err, ErrRoleDeleted) {
		t.Fatalf("err=%v, want ErrRoleDeleted", err)
	}
	if err := role.Disable("actor-2", role.Version); !errors.Is(err, ErrRoleDeleted) {
		t.Fatalf("err=%v, want ErrRoleDeleted", err)
	}
	if err := role.Enable("actor-2", role.Version); !errors.Is(err, ErrRoleDeleted) {
		t.Fatalf("err=%v, want ErrRoleDeleted", err)
	}
	if err := role.Delete("actor-2", role.Version); !errors.Is(err, ErrRoleDeleted) {
		t.Fatalf("err=%v, want ErrRoleDeleted", err)
	}
}

func TestReplaceDataScopes(t *testing.T) {
	role := mustNewRole(t, nil)
	orderScope, err := NewDataScope("order", DataScopeAll, nil)
	if err != nil {
		t.Fatal(err)
	}
	fileScope, err := NewDataScope("file", DataScopeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := role.ReplaceDataScopes([]DataScope{orderScope, fileScope}, "actor-2", role.Version); err != nil {
		t.Fatal(err)
	}
	if scope, ok := role.DataScopeFor("order"); !ok || scope.Type != DataScopeAll {
		t.Fatalf("scope=%+v ok=%v", scope, ok)
	}
	sorted := role.SortedDataScopes()
	if len(sorted) != 2 || sorted[0].Resource != "file" || sorted[1].Resource != "order" {
		t.Fatalf("sorted=%+v", sorted)
	}

	// 重复 resource 必须拒绝，避免「最后一条静默覆盖」。
	if err := role.ReplaceDataScopes([]DataScope{orderScope, orderScope}, "actor-2", role.Version); !errors.Is(err, ErrInvalidDataScope) {
		t.Fatalf("err=%v, want ErrInvalidDataScope", err)
	}

	// B 端禁止 all。
	biz := mustNewBizRole(t, nil)
	if err := biz.ReplaceDataScopes([]DataScope{orderScope}, "actor-1", biz.Version); !errors.Is(err, ErrInvalidDataScope) {
		t.Fatalf("err=%v, want ErrInvalidDataScope", err)
	}
}

func TestMissingDataScopes(t *testing.T) {
	role := mustNewRole(t, nil)
	missing := role.MissingDataScopes([]string{"order", "file", "order", ""})
	if len(missing) != 2 || missing[0] != "file" || missing[1] != "order" {
		t.Fatalf("missing=%v", missing)
	}

	orderScope, err := NewDataScope("order", DataScopeAll, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := role.ReplaceDataScopes([]DataScope{orderScope}, "actor-1", role.Version); err != nil {
		t.Fatal(err)
	}
	if missing := role.MissingDataScopes([]string{"order"}); len(missing) != 0 {
		t.Fatalf("missing=%v", missing)
	}

	// 配置了 * 时应覆盖所有资源。
	allScope, err := NewDataScope(ResourceAll, DataScopeAll, nil)
	if err != nil {
		t.Fatal(err)
	}
	wildcard := mustNewRole(t, nil)
	if err := wildcard.ReplaceDataScopes([]DataScope{allScope}, "actor-1", wildcard.Version); err != nil {
		t.Fatal(err)
	}
	if missing := wildcard.MissingDataScopes([]string{"order", "file"}); len(missing) != 0 {
		t.Fatalf("missing=%v", missing)
	}
}

func TestValidateRoleCodeAndName(t *testing.T) {
	validCodes := []string{"ab", "ops_readonly", "merchant_owner", "a1"}
	for _, code := range validCodes {
		if err := ValidateRoleCode(code); err != nil {
			t.Fatalf("code %q: %v", code, err)
		}
	}
	invalidCodes := []string{"", "a", "A_b", "role-code", "1role", strings.Repeat("a", MaxRoleCodeLen+1)}
	for _, code := range invalidCodes {
		if err := ValidateRoleCode(code); !errors.Is(err, ErrInvalidRoleCode) {
			t.Fatalf("code %q: err=%v", code, err)
		}
	}
	if err := ValidateRoleName("ok"); err != nil {
		t.Fatalf("name: %v", err)
	}
}
