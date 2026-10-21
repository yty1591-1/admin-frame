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

// 模拟鉴权原因码（稳定值，便于前端展示与排障）。
const (
	ReasonAllowed           = "permission_granted"
	ReasonPermissionNotHeld = "permission_not_granted"
	ReasonUnknownPermission = "unknown_permission"
	ReasonNoActiveRole      = "no_active_role"
	ReasonEmptySnapshot     = "empty_snapshot"
)

// QueryService 是 IAM 只读查询用例（角色 / 绑定 / 权限目录 / 模拟鉴权）。
type QueryService struct {
	roles     domain.RoleRepository
	bindings  domain.BindingRepository
	snapshots domain.SnapshotRepository
}

// NewQueryService 构造查询用例；只读依赖缺失时返回错误。
func NewQueryService(roles domain.RoleRepository, bindings domain.BindingRepository, snapshots domain.SnapshotRepository) (*QueryService, error) {
	if roles == nil || bindings == nil || snapshots == nil {
		return nil, apperr.Internal("iam query repositories are required", nil)
	}
	return &QueryService{roles: roles, bindings: bindings, snapshots: snapshots}, nil
}

// RoleDetail 是角色详情（含绑定数量，用于删除前提示）。
type RoleDetail struct {
	Role         *domain.Role
	BindingCount int
}

// GetRole 按 ID 加载角色并执行端别 / 商户隔离。
//
// channel 为空表示内部调用（A 端管理）；非空时跨端、跨商户一律 404（SEC-14）。
func (s *QueryService) GetRole(ctx context.Context, roleID string, channel domain.Channel, merchantID string) (*RoleDetail, error) {
	role, err := s.roles.FindByID(ctx, roleID)
	if err != nil {
		return nil, err
	}
	if role.IsDeleted() {
		return nil, apperr.NotFound("role not found")
	}
	if channel != "" && role.Channel != channel {
		return nil, apperr.NotFound("role not found")
	}
	if merchantID != "" && role.MerchantID != merchantID {
		return nil, apperr.NotFound("role not found")
	}
	bindings, err := s.bindings.ListByRole(ctx, role.ID)
	if err != nil {
		return nil, err
	}
	return &RoleDetail{Role: role, BindingCount: len(bindings)}, nil
}

// ListRoles 分页列举角色；Limit 上限 100（API-05）。
func (s *QueryService) ListRoles(ctx context.Context, filter domain.RoleFilter) ([]*domain.Role, error) {
	if err := validateRolePage(&filter); err != nil {
		return nil, err
	}
	return s.roles.List(ctx, filter)
}

// CountRoles 返回与 ListRoles 同条件的角色总数，供分页 total 与配额校验。
//
// 计数不分页，但仍校验 page_size 上限，避免调用方以为可以绕过 API-05。
func (s *QueryService) CountRoles(ctx context.Context, filter domain.RoleFilter) (int, error) {
	if err := validateRolePage(&filter); err != nil {
		return 0, err
	}
	return s.roles.Count(ctx, filter)
}

// validateRolePage 复用分页上限校验，避免 List / 后续接口规则漂移。
func validateRolePage(filter *domain.RoleFilter) error {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		return apperr.BadRequest("page_size exceeds maximum of 100")
	}
	return nil
}

// ListBindings 列举主体有效绑定。
func (s *QueryService) ListBindings(ctx context.Context, query domain.SubjectQuery) ([]*domain.Binding, error) {
	return s.bindings.ListBySubject(ctx, query)
}

// ListBindingsByRole 列举角色下的全部绑定（含已过期），供管理端排查引用关系。
func (s *QueryService) ListBindingsByRole(ctx context.Context, roleID string) ([]*domain.Binding, error) {
	return s.bindings.ListByRole(ctx, roleID)
}

// SubjectSnapshot 返回主体有效权限快照，供权限查看与模拟鉴权复用。
func (s *QueryService) SubjectSnapshot(ctx context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
	return s.snapshots.Load(ctx, query)
}

// PermissionFilter 是权限目录过滤条件。
type PermissionFilter struct {
	// Channel 端别过滤；空表示全部。
	Channel domain.Channel
	// Keyword 按 code / name 做包含匹配（大小写不敏感）。
	Keyword string
}

// Permissions 返回过滤后的权限目录（顺序与 catalog 一致）。
func (s *QueryService) Permissions(filter PermissionFilter) []catalog.Entry {
	all := catalog.All()
	out := make([]catalog.Entry, 0, len(all))
	keyword := strings.ToLower(strings.TrimSpace(filter.Keyword))
	for _, entry := range all {
		if filter.Channel != "" && entry.Channel != catalog.ChannelBoth && string(entry.Channel) != string(filter.Channel) {
			continue
		}
		if keyword != "" &&
			!strings.Contains(strings.ToLower(string(entry.Code)), keyword) &&
			!strings.Contains(strings.ToLower(entry.Name), keyword) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// PermissionNode 是权限目录树节点，供菜单 / 按钮渲染。
type PermissionNode struct {
	Entry    catalog.Entry
	Children []*PermissionNode
}

// PermissionTree 返回过滤后的权限树。
//
// 父节点被过滤掉时，子节点提升为根，避免出现「孤儿节点」在界面上丢失。
func (s *QueryService) PermissionTree(filter PermissionFilter) []*PermissionNode {
	entries := s.Permissions(filter)
	nodes := make(map[iam.PermissionCode]*PermissionNode, len(entries))
	for _, entry := range entries {
		nodes[entry.Code] = &PermissionNode{Entry: entry}
	}
	roots := make([]*PermissionNode, 0, len(entries))
	for _, entry := range entries {
		node := nodes[entry.Code]
		if parent, ok := nodes[entry.ParentCode]; ok && entry.ParentCode != "" {
			parent.Children = append(parent.Children, node)
			continue
		}
		roots = append(roots, node)
	}
	return roots
}

// SimulateQuery 是模拟鉴权入参。
type SimulateQuery struct {
	Channel     domain.Channel
	SubjectType domain.SubjectType
	SubjectID   string
	MerchantID  string
	Permission  iam.PermissionCode
}

// SimulateResult 是模拟鉴权结果；只返回最小必要信息。
type SimulateResult struct {
	Allowed         bool
	Reason          string
	MatchedRoles    []string
	DataScope       *domain.DataScope
	SnapshotVersion int64
}

// Simulate 以快照为准计算是否允许某权限点，不改变任何授权状态。
//
// 目录中不存在的权限码直接判为不允许（reason=unknown_permission），
// 避免把「权限码拼错」误判成「服务端拒绝」而浪费排查时间。
func (s *QueryService) Simulate(ctx context.Context, query SimulateQuery) (*SimulateResult, error) {
	if _, ok := catalog.ByCode(query.Permission); !ok {
		return &SimulateResult{Allowed: false, Reason: ReasonUnknownPermission}, nil
	}
	snapshot, err := s.snapshots.Load(ctx, domain.SubjectQuery{
		Channel:     query.Channel,
		SubjectType: query.SubjectType,
		SubjectID:   query.SubjectID,
		MerchantID:  query.MerchantID,
	})
	if err != nil {
		return nil, err
	}
	result := &SimulateResult{
		MatchedRoles:    slices.Clone(snapshot.Roles),
		SnapshotVersion: snapshot.Version,
	}
	if len(snapshot.Roles) == 0 {
		if snapshot.Version == 0 {
			// 从未生成过快照：主体没有任何授权记录。
			result.Reason = ReasonEmptySnapshot
			return result, nil
		}
		result.Reason = ReasonNoActiveRole
		return result, nil
	}
	if !snapshot.HasPermission(query.Permission) {
		result.Reason = ReasonPermissionNotHeld
		return result, nil
	}
	result.Allowed = true
	result.Reason = ReasonAllowed
	resource := strings.SplitN(string(query.Permission), ":", 2)[0]
	if scope, ok := snapshot.ScopeFor(resource); ok {
		scoped := scope
		result.DataScope = &scoped
	}
	return result, nil
}
