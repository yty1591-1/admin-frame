package persistence

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemoryRoleConstraintRepository 是基于内存 map 的角色前置约束仓储。
//
// 仅用于单测与 local / demo；生产使用 SQLRoleConstraintRepository。
type MemoryRoleConstraintRepository struct {
	mu     sync.RWMutex
	byRole map[string][]string
}

// NewMemoryRoleConstraintRepository 创建空的前置约束仓储。
func NewMemoryRoleConstraintRepository() *MemoryRoleConstraintRepository {
	return &MemoryRoleConstraintRepository{byRole: make(map[string][]string)}
}

// ListByRole 返回指定角色的前置约束；无约束时返回空集合。
func (r *MemoryRoleConstraintRepository) ListByRole(
	_ context.Context, roleID string,
) (domain.RoleConstraint, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	required := r.byRole[roleID]
	if required == nil {
		required = make([]string, 0)
	}
	return domain.RoleConstraint{RoleID: roleID, RequiredRoleIDs: slices.Clone(required)}, nil
}

// ListByRoles 按入参顺序批量返回前置约束（无约束的角色返回空集合）。
func (r *MemoryRoleConstraintRepository) ListByRoles(
	_ context.Context, roleIDs []string,
) ([]domain.RoleConstraint, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]domain.RoleConstraint, 0, len(roleIDs))
	seen := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		if _, dup := seen[roleID]; dup {
			continue
		}
		seen[roleID] = struct{}{}
		required := r.byRole[roleID]
		if required == nil {
			required = make([]string, 0)
		}
		out = append(out, domain.RoleConstraint{RoleID: roleID, RequiredRoleIDs: slices.Clone(required)})
	}
	return out, nil
}

// ListDependents 返回把 roleID 作为前置角色的角色 ID，按 ID 排序。
func (r *MemoryRoleConstraintRepository) ListDependents(_ context.Context, roleID string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0)
	for dependent, required := range r.byRole {
		if slices.Contains(required, roleID) {
			out = append(out, dependent)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Replace 全量替换角色的前置角色；自引用与重复由领域层拒绝。
func (r *MemoryRoleConstraintRepository) Replace(
	_ context.Context, role *domain.Role, requiredRoleIDs []string, actor string,
) error {
	if role == nil {
		return apperr.BadRequest("role is required")
	}
	if strings.TrimSpace(actor) == "" {
		return apperr.BadRequest("actor is required")
	}
	constraint, err := domain.NewRoleConstraint(role.ID, requiredRoleIDs)
	if err != nil {
		return err
	}
	current := constraint.Sorted()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(current) == 0 {
		delete(r.byRole, role.ID)
		return nil
	}
	r.byRole[role.ID] = current
	return nil
}
