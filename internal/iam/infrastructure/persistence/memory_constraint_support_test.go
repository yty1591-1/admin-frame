package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

func TestMemoryRoleRepositoryListAncestors(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleRepository()
	root := newTestRole(t, "role-1", "root_role", nil)
	child := newTestRole(t, "role-2", "child_role", func(p *domain.NewRoleParams) { p.ParentID = "role-1" })
	grand := newTestRole(t, "role-3", "grand_role", func(p *domain.NewRoleParams) { p.ParentID = "role-2" })
	for _, role := range []*domain.Role{root, child, grand} {
		if err := repo.Save(ctx, role, 0); err != nil {
			t.Fatalf("save %s: %v", role.ID, err)
		}
	}

	ancestors, err := repo.ListAncestors(ctx, "role-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(ancestors) != 2 || ancestors[0].ID != "role-1" || ancestors[1].ID != "role-2" {
		t.Fatalf("ancestors=%v", roleIDs(ancestors))
	}

	ancestors, err = repo.ListAncestors(ctx, "role-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ancestors) != 0 {
		t.Fatalf("root ancestors=%v", roleIDs(ancestors))
	}

	// 软删中间层后祖先链在此断开，更深层角色不可见。
	if err := child.Delete("actor-1", child.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, child, 1); err != nil {
		t.Fatal(err)
	}
	ancestors, err = repo.ListAncestors(ctx, "role-3")
	if err != nil {
		t.Fatal(err)
	}
	if len(ancestors) != 0 {
		t.Fatalf("deleted ancestor must break chain: %v", roleIDs(ancestors))
	}
}

func TestMemoryBindingRepositoryCountEffectiveByRole(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryBindingRepository()
	now := time.Now().UTC()
	future := now.Add(time.Hour)

	bindings := []*domain.Binding{
		newTestBinding(t, "binding-1", "u-1", "role-1", nil),
		newTestBinding(t, "binding-2", "u-2", "role-1", nil),
		newTestBinding(t, "binding-3", "u-3", "role-1", func(p *domain.NewBindingParams) {
			p.ExpiresAt = &future
		}),
		newTestBinding(t, "binding-4", "u-1", "role-2", nil),
	}
	for _, binding := range bindings {
		if err := repo.Save(ctx, binding); err != nil {
			t.Fatalf("save %s: %v", binding.ID, err)
		}
	}

	// 查询时刻晚于 binding-3 的有效期：计数必须排除过期绑定。
	count, err := repo.CountEffectiveByRole(ctx, domain.ChannelAdmin, "", "role-1", now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count=%d, want 2 (expired binding must be excluded)", count)
	}

	list, err := repo.ListBySubjectForUpdate(ctx, domain.SubjectQuery{
		Channel: domain.ChannelAdmin, SubjectType: domain.SubjectStaff, SubjectID: "u-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("for-update list=%d, want 2", len(list))
	}
}

func TestMemoryRoleConstraintRepositoryListDependents(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleConstraintRepository()
	base := newTestRole(t, "role-1", "base_role", nil)
	dependentA := newTestRole(t, "role-2", "dependent_a", nil)
	dependentB := newTestRole(t, "role-3", "dependent_b", nil)

	if err := repo.Replace(ctx, dependentB, []string{"role-1"}, "actor-1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Replace(ctx, dependentA, []string{"role-1"}, "actor-1"); err != nil {
		t.Fatal(err)
	}

	dependents, err := repo.ListDependents(ctx, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 2 || dependents[0] != "role-2" || dependents[1] != "role-3" {
		t.Fatalf("dependents=%v", dependents)
	}

	dependents, err = repo.ListDependents(ctx, "role-99")
	if err != nil {
		t.Fatal(err)
	}
	if len(dependents) != 0 {
		t.Fatalf("unexpected dependents=%v", dependents)
	}
}

// roleIDs 提取角色 ID 列表，便于失败信息可读。
func roleIDs(roles []*domain.Role) []string {
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		out = append(out, role.ID)
	}
	return out
}
