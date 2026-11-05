package app_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// TestQueryServiceReadPaths 覆盖角色计数、绑定查询与主体快照的只读路径。
func TestQueryServiceReadPaths(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	role := createTestRole(t, f, "ops_lead")
	binding, err := f.bindingSvc.Bind(ctx, app.BindCommand{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "2001",
		RoleID:      role.ID,
		Actor:       "1001",
		Reason:      "测试绑定",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	count, err := f.querySvc.CountRoles(ctx, domain.RoleFilter{
		Channel: domain.ChannelAdmin,
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("CountRoles: %v", err)
	}
	if count != 1 {
		t.Fatalf("role count = %d, want 1", count)
	}

	query := domain.SubjectQuery{
		Channel:     domain.ChannelAdmin,
		SubjectType: domain.SubjectStaff,
		SubjectID:   "2001",
	}
	bindings, err := f.querySvc.ListBindings(ctx, query)
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].ID != binding.ID {
		t.Fatalf("bindings = %+v", bindings)
	}

	byRole, err := f.querySvc.ListBindingsByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListBindingsByRole: %v", err)
	}
	if len(byRole) != 1 || byRole[0].ID != binding.ID {
		t.Fatalf("bindings by role = %+v", byRole)
	}

	snapshot, err := f.querySvc.SubjectSnapshot(ctx, query)
	if err != nil {
		t.Fatalf("SubjectSnapshot: %v", err)
	}
	if snapshot.SubjectID != "2001" || !snapshot.HasPermission(iam.PermRoleRead) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
