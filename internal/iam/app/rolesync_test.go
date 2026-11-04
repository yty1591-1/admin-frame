package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/domain"

	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
	tenantdomain "github.com/admin-frame/admin-frame/internal/tenant/domain"
)

// roleSyncSeqID 是测试用的自增 ID 生成器。
type roleSyncSeqID struct {
	next int
}

// Next 返回自增 ID。
func (g *roleSyncSeqID) Next() (int64, error) {
	g.next++
	return int64(g.next), nil
}

// NextString 返回自增 ID 的字符串形式。
func (g *roleSyncSeqID) NextString() (string, error) {
	v, err := g.Next()
	return "gen-" + strconv.FormatInt(v, 10), err
}

// newRoleSyncFixture 构造内存 IAM 仓储上的适配器与租户上下文。
func newRoleSyncFixture(t *testing.T) (roleSyncAdapter, domain.RoleRepository, domain.BindingRepository) {
	t.Helper()
	roles := persistence.NewMemoryRoleRepository()
	bindings := persistence.NewMemoryBindingRepository()
	adapter, err := NewRoleSyncer(roles, bindings, &roleSyncSeqID{})
	if err != nil {
		t.Fatalf("NewRoleSyncer() error = %v", err)
	}
	return adapter.(roleSyncAdapter), roles, bindings
}

// mustEnsure 便捷物化模板角色。
func mustEnsure(t *testing.T, a roleSyncAdapter, tenantID string) {
	t.Helper()
	if err := a.EnsureTemplates(context.Background(), tenantID); err != nil {
		t.Fatalf("EnsureTemplates() error = %v", err)
	}
}

// templateBinding 统计主体当前生效的模板绑定角色编码集合。
func templateBinding(t *testing.T, a roleSyncAdapter, bindings domain.BindingRepository, tenantID, subjectID string) map[string]bool {
	t.Helper()
	list, err := bindings.ListBySubject(context.Background(), domain.SubjectQuery{
		Channel:     domain.ChannelBiz,
		SubjectType: domain.SubjectMerchantStaff,
		SubjectID:   subjectID,
		MerchantID:  tenantID,
	})
	if err != nil {
		t.Fatalf("ListBySubject() error = %v", err)
	}
	out := map[string]bool{}
	for _, b := range list {
		if b.RevokedAt != nil {
			continue
		}
		role, err := a.roles.FindByID(context.Background(), b.RoleID)
		if err != nil {
			t.Fatalf("FindByID(%s) error = %v", b.RoleID, err)
		}
		out[role.Code] = true
	}
	return out
}

// TestIAMEnsureTemplatesIdempotent 验证模板角色物化幂等。
func TestIAMEnsureTemplatesIdempotent(t *testing.T) {
	a, roles, _ := newRoleSyncFixture(t)
	ctx := context.Background()
	mustEnsure(t, a, "t1")
	mustEnsure(t, a, "t1") // 重复执行不报错、不重复建

	for _, code := range []string{"tenant_owner", "tenant_admin", "tenant_member"} {
		role, err := roles.FindByCode(ctx, domain.ChannelBiz, "t1", code)
		if err != nil {
			t.Fatalf("FindByCode(%s) error = %v", code, err)
		}
		if role.Type != domain.RoleTypeSystem || role.MerchantID != "t1" {
			t.Fatalf("role %s = %+v", code, role)
		}
	}
}

// TestIAMSyncRoleAddAndChange 验证绑定多退少补：加成员绑定、改角色换绑。
func TestIAMSyncRoleAddAndChange(t *testing.T) {
	a, _, bindings := newRoleSyncFixture(t)
	ctx := context.Background()
	mustEnsure(t, a, "t1")

	owner := tenantdomain.MemberRoleOwner
	if err := a.SyncRole(ctx, "t1", "u1", &owner); err != nil {
		t.Fatalf("SyncRole(owner) error = %v", err)
	}
	if got := templateBinding(t, a, bindings, "t1", "u1"); !got["tenant_owner"] || len(got) != 1 {
		t.Fatalf("bindings after add = %v, want [tenant_owner]", got)
	}

	// 改角色：owner → member，旧模板解绑、新模板绑定。
	member := tenantdomain.MemberRoleMember
	if err := a.SyncRole(ctx, "t1", "u1", &member); err != nil {
		t.Fatalf("SyncRole(member) error = %v", err)
	}
	got := templateBinding(t, a, bindings, "t1", "u1")
	if got["tenant_owner"] || !got["tenant_member"] {
		t.Fatalf("bindings after change = %v, want only [tenant_member]", got)
	}

	// 幂等：重复同步同一角色不产生变化。
	if err := a.SyncRole(ctx, "t1", "u1", &member); err != nil {
		t.Fatalf("SyncRole(again) error = %v", err)
	}
	if got := templateBinding(t, a, bindings, "t1", "u1"); len(got) != 1 || !got["tenant_member"] {
		t.Fatalf("bindings after idempotent sync = %v", got)
	}
}

// TestIAMSyncRoleDisableUnbinds 验证停用成员解绑全部模板角色。
func TestIAMSyncRoleDisableUnbinds(t *testing.T) {
	a, _, bindings := newRoleSyncFixture(t)
	ctx := context.Background()
	mustEnsure(t, a, "t1")

	admin := tenantdomain.MemberRoleAdmin
	if err := a.SyncRole(ctx, "t1", "u1", &admin); err != nil {
		t.Fatalf("SyncRole(admin) error = %v", err)
	}
	if err := a.SyncRole(ctx, "t1", "u1", nil); err != nil {
		t.Fatalf("SyncRole(nil) error = %v", err)
	}
	if got := templateBinding(t, a, bindings, "t1", "u1"); len(got) != 0 {
		t.Fatalf("bindings after disable = %v, want empty", got)
	}

	// 重新启用 → 重绑（撤销行之后新增生效行）。
	if err := a.SyncRole(ctx, "t1", "u1", &admin); err != nil {
		t.Fatalf("SyncRole(re-enable) error = %v", err)
	}
	if got := templateBinding(t, a, bindings, "t1", "u1"); len(got) != 1 || !got["tenant_admin"] {
		t.Fatalf("bindings after re-enable = %v, want [tenant_admin]", got)
	}
}

// TestIAMSyncRoleSkipsCustomBindings 验证自建角色（custom）绑定不受同步影响。
func TestIAMSyncRoleSkipsCustomBindings(t *testing.T) {
	a, roles, bindings := newRoleSyncFixture(t)
	ctx := context.Background()
	mustEnsure(t, a, "t1")

	// 商户自建角色 + 手工绑定。
	custom, err := domain.NewRole(domain.NewRoleParams{
		ID: "r-custom-1", Channel: domain.ChannelBiz, Scope: domain.RoleScopeMerchant,
		MerchantID: "t1", Code: "ops_lead", Name: "运维组长", Type: domain.RoleTypeCustom,
		Actor: "ops",
	})
	if err != nil {
		t.Fatalf("NewRole(custom) error = %v", err)
	}
	if err := roles.Save(ctx, custom, 0); err != nil {
		t.Fatalf("Save(custom) error = %v", err)
	}
	binding, err := domain.NewBinding(domain.NewBindingParams{
		ID: "b-custom-1", Channel: domain.ChannelBiz, SubjectType: domain.SubjectMerchantStaff,
		SubjectID: "u1", MerchantID: "t1", RoleID: custom.ID, GrantedBy: "ops",
	})
	if err != nil {
		t.Fatalf("NewBinding(custom) error = %v", err)
	}
	if err := bindings.Save(ctx, binding); err != nil {
		t.Fatalf("Save(custom binding) error = %v", err)
	}

	// 同步到 member：custom 绑定必须保留。
	member := tenantdomain.MemberRoleMember
	if err := a.SyncRole(ctx, "t1", "u1", &member); err != nil {
		t.Fatalf("SyncRole(member) error = %v", err)
	}
	got := templateBinding(t, a, bindings, "t1", "u1")
	if !got["tenant_member"] || !got["ops_lead"] {
		t.Fatalf("bindings = %v, want [tenant_member ops_lead]", got)
	}

	// 停用：custom 绑定仍保留。
	if err := a.SyncRole(ctx, "t1", "u1", nil); err != nil {
		t.Fatalf("SyncRole(nil) error = %v", err)
	}
	if got := templateBinding(t, a, bindings, "t1", "u1"); !got["ops_lead"] || got["tenant_member"] {
		t.Fatalf("bindings after disable = %v, want only [ops_lead]", got)
	}
}

// TestIAMSyncRoleMissingTemplateFails 验证目标模板角色缺失时显式失败（装配缺陷）。
func TestIAMSyncRoleMissingTemplateFails(t *testing.T) {
	a, _, _ := newRoleSyncFixture(t)
	// 未调用 EnsureTemplates。
	owner := tenantdomain.MemberRoleOwner
	if err := a.SyncRole(context.Background(), "t-none", "u1", &owner); err == nil {
		t.Fatal("模板角色缺失应报错（fail closed）")
	}
	// 停用（nil）不依赖模板角色，应成功。
	if err := a.SyncRole(context.Background(), "t-none", "u1", nil); err != nil {
		t.Fatalf("SyncRole(nil) error = %v", err)
	}
}
