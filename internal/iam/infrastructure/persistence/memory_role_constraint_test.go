package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

func TestMemoryRoleConstraintRepositoryReplaceAndList(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleConstraintRepository()
	role := newTestRole(t, "role-1", "ops_read", nil)

	if err := repo.Replace(ctx, role, []string{"role-3", "role-2"}, "actor-2"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	constraint, err := repo.ListByRole(ctx, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(constraint.RequiredRoleIDs) != 2 ||
		constraint.RequiredRoleIDs[0] != "role-2" ||
		constraint.RequiredRoleIDs[1] != "role-3" {
		t.Fatalf("required=%v", constraint.RequiredRoleIDs)
	}

	batch, err := repo.ListByRoles(ctx, []string{role.ID, "role-9", role.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("batch=%v", batch)
	}
	if len(batch[0].RequiredRoleIDs) != 2 {
		t.Fatalf("first=%+v", batch[0])
	}
	if len(batch[1].RequiredRoleIDs) != 0 {
		t.Fatalf("second=%+v", batch[1])
	}

	// 全量替换为空表示清除约束。
	if err := repo.Replace(ctx, role, nil, "actor-2"); err != nil {
		t.Fatalf("Replace empty: %v", err)
	}
	cleared, err := repo.ListByRole(ctx, role.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !cleared.Empty() {
		t.Fatalf("cleared=%+v", cleared)
	}
}

func TestMemoryRoleConstraintRepositoryValidation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRoleConstraintRepository()
	role := newTestRole(t, "role-1", "ops_read", nil)

	if err := repo.Replace(ctx, nil, nil, "actor-2"); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("nil role err=%v, want bad request", err)
	}
	if err := repo.Replace(ctx, role, nil, ""); !errors.Is(err, apperr.ErrBadRequest) {
		t.Fatalf("missing actor err=%v, want bad request", err)
	}
	if err := repo.Replace(ctx, role, []string{role.ID}, "actor-2"); !errors.Is(err, domain.ErrInvalidRoleConstraint) {
		t.Fatalf("self prerequisite err=%v, want ErrInvalidRoleConstraint", err)
	}
}
