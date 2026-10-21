package persistence

import (
	"context"
	"slices"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/catalog"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// MemorySnapshotRepository 是内存版鉴权快照实现（local / dev / 单测）。
//
// 语义与 SQLSnapshotRepository 保持一致：只聚合 active 且未软删的角色，
// 只保留 active 且端别匹配的权限点，数据范围按资源取并集。
// 内存实现按绑定逐个加载角色（N+1），仅供单机；生产必须使用 SQL 实现。
type MemorySnapshotRepository struct {
	roles    *MemoryRoleRepository
	bindings *MemoryBindingRepository
}

// NewMemorySnapshotRepository 组合内存角色 / 绑定仓储构造快照仓储。
func NewMemorySnapshotRepository(roles *MemoryRoleRepository, bindings *MemoryBindingRepository) *MemorySnapshotRepository {
	return &MemorySnapshotRepository{roles: roles, bindings: bindings}
}

// Load 加载主体有效权限快照；无绑定返回空快照（调用方默认拒绝）。
func (r *MemorySnapshotRepository) Load(ctx context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
	if r == nil || r.roles == nil || r.bindings == nil {
		return nil, apperr.Unavailable("iam snapshot repository is not configured")
	}
	bindings, err := r.bindings.ListBySubject(ctx, query)
	if err != nil {
		return nil, err
	}

	roleSet := make(map[string]struct{})
	permissionSet := make(map[iam.PermissionCode]struct{})
	rawScopes := make([]domain.DataScope, 0)
	var version int64

	for _, binding := range bindings {
		role, findErr := r.roles.FindByID(ctx, binding.RoleID)
		if findErr != nil {
			// 绑定指向已删除角色：跳过而不是失败，避免一条脏绑定拖垮整次鉴权。
			continue
		}
		if !role.IsActive() || role.Channel != query.Channel {
			continue
		}
		roleSet[role.Code] = struct{}{}
		if role.Version > version {
			version = role.Version
		}
		for _, code := range role.PermissionCodes() {
			entry, ok := catalog.ByCode(code)
			if !ok || entry.Status != catalog.StatusActive {
				continue
			}
			if entry.Channel != catalog.ChannelBoth && string(entry.Channel) != string(query.Channel) {
				continue
			}
			permissionSet[code] = struct{}{}
		}
		rawScopes = append(rawScopes, role.SortedDataScopes()...)
	}

	roles := make([]string, 0, len(roleSet))
	for code := range roleSet {
		roles = append(roles, code)
	}
	slices.Sort(roles)

	permissions := make([]iam.PermissionCode, 0, len(permissionSet))
	for code := range permissionSet {
		permissions = append(permissions, code)
	}
	slices.Sort(permissions)

	scopes := make([]domain.DataScope, 0)
	if len(rawScopes) > 0 {
		scopes, err = domain.MergeDataScopes(rawScopes)
		if err != nil {
			return nil, err
		}
	}
	return &domain.SubjectSnapshot{
		Channel:     query.Channel,
		SubjectType: query.SubjectType,
		SubjectID:   query.SubjectID,
		MerchantID:  query.MerchantID,
		Roles:       roles,
		Permissions: permissions,
		Scopes:      scopes,
		Version:     version,
		GeneratedAt: time.Now().UTC(),
	}, nil
}
