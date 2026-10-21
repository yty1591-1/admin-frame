package app

import (
	"context"
	"slices"
	"strings"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/catalog"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// validateGrant 校验权限点是否可授予给定端别，并返回数据范围必填的资源列表。
//
// 校验项（docs/rbac-iam.md §6.2）：
//   - 权限点必须在目录中，避免拼写错误静默落库；
//   - 已弃用（deprecated）权限点禁止新授，只保留既有授权；
//   - 端别必须匹配：A 端不可授予仅 B 端权限点，反之亦然；
//   - 涉及数据的权限点必须有对应资源的数据范围，否则等于越权读全量。
func validateGrant(channel domain.Channel, codes []iam.PermissionCode) ([]string, error) {
	if err := channel.Validate(); err != nil {
		return nil, err
	}
	required := make([]string, 0, len(codes))
	for _, code := range codes {
		entry, ok := catalog.ByCode(code)
		if !ok {
			return nil, apperr.BadRequest("unknown permission code: " + string(code))
		}
		if entry.Status != catalog.StatusActive {
			return nil, apperr.BadRequest("permission code is deprecated and cannot be granted: " + string(code))
		}
		if entry.Channel != catalog.ChannelBoth && string(entry.Channel) != string(channel) {
			return nil, apperr.BadRequest("permission code " + string(code) + " is not assignable to channel " + string(channel))
		}
		if entry.DataBearing {
			required = append(required, entry.Resource())
		}
	}
	return required, nil
}

// requireDataScopes 校验授予的 data_bearing 权限都配置了数据范围。
//
// 领域层只做结构一致性检查；这里把目录推导出的必填资源与角色的数据范围对齐，
// 缺失时一次性列出全部资源，便于调用方一次修正。
func requireDataScopes(role *domain.Role, required []string) error {
	missing := role.MissingDataScopes(required)
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return apperr.BadRequest("data_scopes required for resources: " + strings.Join(missing, ","))
}

// normalizeDataScopes 克隆并去重数据范围；重复资源直接拒绝（不静默覆盖）。
func normalizeDataScopes(channel domain.Channel, scopes []domain.DataScope) ([]domain.DataScope, error) {
	out := make([]domain.DataScope, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if err := scope.Validate(channel); err != nil {
			return nil, err
		}
		if _, dup := seen[scope.Resource]; dup {
			return nil, apperr.BadRequest("duplicate data scope for resource: " + scope.Resource)
		}
		seen[scope.Resource] = struct{}{}
		scope.MerchantIDs = slices.Clone(scope.MerchantIDs)
		out = append(out, scope)
	}
	return out, nil
}

// normalizeStatus 校验并归一化 PATCH 的状态取值。
func normalizeStatus(status string) (domain.RoleStatus, error) {
	switch domain.RoleStatus(strings.TrimSpace(status)) {
	case domain.RoleStatusActive:
		return domain.RoleStatusActive, nil
	case domain.RoleStatusDisabled:
		return domain.RoleStatusDisabled, nil
	default:
		return "", apperr.BadRequest("status must be active or disabled")
	}
}

// permissionCodeStrings 把权限码切片转为字符串切片，供审计与响应。
func permissionCodeStrings(codes []iam.PermissionCode) []string {
	out := make([]string, len(codes))
	for i, code := range codes {
		out[i] = string(code)
	}
	return out
}

// rolePermissionStrings 返回角色已授予权限码的确定性顺序。
func rolePermissionStrings(role *domain.Role) []string {
	return permissionCodeStrings(role.PermissionCodes())
}

// roleForGrant 加载角色并校验版本；用于授权与删除前的统一前置检查。
func roleForGrant(ctx context.Context, repo domain.RoleRepository, roleID string, version int64) (*domain.Role, error) {
	role, err := repo.FindByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if version != role.Version {
		return nil, apperr.Conflict("role version conflict: reload and retry")
	}
	return role, nil
}
