package iam_test

import (
	"context"
	"slices"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/infrastructure/persistence"
)

// TestSQLRoleConstraintRoundTrip 覆盖 RBAC2 前置约束与基数上限的真实 SQL 读写。
//
// 仅在 IAM_TEST_MYSQL_DSN 存在时执行；本地无 MySQL / Docker 时由 testDB(t) 跳过。
func TestSQLRoleConstraintRoundTrip(t *testing.T) {
	sqlDB := testDB(t)
	ctx := context.Background()
	roles := persistence.NewSQLRoleRepository(sqlDB)
	constraints := persistence.NewSQLRoleConstraintRepository(sqlDB)

	target := newRole(t, roles, "9101", "constraint_target_sql", nil, nil)
	first := newRole(t, roles, "9102", "constraint_first_sql", nil, nil)
	second := newRole(t, roles, "9103", "constraint_second_sql", nil, nil)

	if err := constraints.Replace(ctx, target, []string{second.ID, first.ID}, "actor-1"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	loaded, err := constraints.ListByRole(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListByRole: %v", err)
	}
	want := []string{first.ID, second.ID}
	if !slices.Equal(loaded.RequiredRoleIDs, want) {
		t.Fatalf("required=%v, want %v", loaded.RequiredRoleIDs, want)
	}

	dependents, err := constraints.ListDependents(ctx, first.ID)
	if err != nil {
		t.Fatalf("ListDependents: %v", err)
	}
	if !slices.Equal(dependents, []string{target.ID}) {
		t.Fatalf("dependents=%v, want [%s]", dependents, target.ID)
	}

	batched, err := constraints.ListByRoles(ctx, []string{target.ID, first.ID})
	if err != nil {
		t.Fatalf("ListByRoles: %v", err)
	}
	if len(batched) != 2 || len(batched[1].RequiredRoleIDs) != 0 {
		t.Fatalf("batched=%+v", batched)
	}

	// 基数上限属于角色聚合，必须经 RoleRepository 持久化并在重载后保持一致。
	limit := 3
	if err := target.SetMaxBindings(&limit, "actor-1", target.Version); err != nil {
		t.Fatalf("SetMaxBindings: %v", err)
	}
	if err := roles.Save(ctx, target, target.Version-1); err != nil {
		t.Fatalf("Save max_bindings: %v", err)
	}
	reloaded, err := roles.FindByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if reloaded.MaxBindings == nil || *reloaded.MaxBindings != limit {
		t.Fatalf("max_bindings=%v, want %d", reloaded.MaxBindings, limit)
	}

	// 清空前置关系；无约束时返回空集合而非 NotFound。
	if err := constraints.Replace(ctx, reloaded, nil, "actor-1"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	cleared, err := constraints.ListByRole(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListByRole after clear: %v", err)
	}
	if len(cleared.RequiredRoleIDs) != 0 {
		t.Fatalf("required=%v, want empty", cleared.RequiredRoleIDs)
	}
}
