package domain

import (
	"slices"
	"strings"
)

// MaxPrerequisitesPerRole 单角色前置条件数量上限，避免绑定期展开过多依赖。
const MaxPrerequisitesPerRole = 16

// RoleConstraint 是角色维度的 RBAC2 前置约束。
//
// 前置语义：主体必须先持有 RequiredRoleIDs 中的全部角色，才能被授予 RoleID；
// 判定基于「有效角色闭包」（绑定角色 + RBAC1 祖先），在 app 层展开。
// 破玻璃角色不能作为前置角色，也不能声明前置，由 app 层结合角色类型校验。
type RoleConstraint struct {
	// RoleID 受约束的角色 ID。
	RoleID string
	// RequiredRoleIDs 必须先持有的角色 ID，永不为 nil。
	RequiredRoleIDs []string
}

// NewRoleConstraint 校验并构造角色前置约束。
func NewRoleConstraint(roleID string, requiredRoleIDs []string) (RoleConstraint, error) {
	if strings.TrimSpace(roleID) == "" {
		return RoleConstraint{}, errInvalid(ErrEmptyInput, "role id is required for constraints")
	}
	if len(requiredRoleIDs) > MaxPrerequisitesPerRole {
		return RoleConstraint{}, errInvalid(ErrInvalidRoleConstraint,
			"role %s has %d prerequisites, exceeds %d",
			roleID, len(requiredRoleIDs), MaxPrerequisitesPerRole)
	}
	seen := make(map[string]struct{}, len(requiredRoleIDs))
	out := make([]string, 0, len(requiredRoleIDs))
	for _, requiredID := range requiredRoleIDs {
		if strings.TrimSpace(requiredID) == "" {
			return RoleConstraint{}, errInvalid(ErrInvalidRoleConstraint, "prerequisite role id is required")
		}
		if requiredID == roleID {
			return RoleConstraint{}, errInvalid(ErrInvalidRoleConstraint,
				"role %s must not require itself", roleID)
		}
		if _, dup := seen[requiredID]; dup {
			return RoleConstraint{}, errInvalid(ErrInvalidRoleConstraint,
				"duplicate prerequisite %q for role %s", requiredID, roleID)
		}
		seen[requiredID] = struct{}{}
		out = append(out, requiredID)
	}
	return RoleConstraint{RoleID: roleID, RequiredRoleIDs: out}, nil
}

// Sorted 返回确定性顺序的前置角色 ID。
func (c RoleConstraint) Sorted() []string {
	out := slices.Clone(c.RequiredRoleIDs)
	slices.Sort(out)
	return out
}

// Empty 报告该角色是否没有前置约束。
func (c RoleConstraint) Empty() bool { return len(c.RequiredRoleIDs) == 0 }
