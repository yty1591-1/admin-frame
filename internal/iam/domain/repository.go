package domain

import (
	"context"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
)

// RoleRepository 是角色聚合的持久化端口，由 infrastructure 实现。
//
// 约定：
//   - 未找到返回 internal/shared/apperr 的 NotFound（errors.Is(err, apperr.ErrNotFound)）；
//   - Save 必须使用 expectedVersion 做乐观锁，冲突返回 apperr 的 Conflict；
//   - 所有方法都必须带端别条件；B 端调用方的 MerchantID 不得为空。
type RoleRepository interface {
	// FindByID 按主键加载角色（含权限与数据范围）。
	FindByID(ctx context.Context, id string) (*Role, error)
	// FindByCode 按端别 + 商户 + 编码加载角色。
	FindByCode(ctx context.Context, channel Channel, merchantID, code string) (*Role, error)
	// Save 创建或更新角色；expectedVersion 为 0 表示创建。
	Save(ctx context.Context, role *Role, expectedVersion int64) error
	// List 按条件分页列举角色。
	List(ctx context.Context, filter RoleFilter) ([]*Role, error)
	// Count 返回满足条件的未删除角色总数，用于分页 total 与 B 端角色配额。
	Count(ctx context.Context, filter RoleFilter) (int, error)
}

// RoleFilter 是角色列表过滤条件。
type RoleFilter struct {
	// Channel 端别过滤；空表示不过滤（仅 A 端内部查询允许）。
	Channel Channel
	// MerchantID 商户过滤；B 端必填。
	MerchantID string
	// Status 状态过滤；空表示全部。
	Status RoleStatus
	// Keyword 按 code / name 模糊匹配；空表示不过滤。
	Keyword string
	// Limit 分页大小，上限 100（API-05）。
	Limit int
	// Offset 偏移量。
	Offset int
}

// BindingRepository 是主体-角色绑定的持久化端口。
type BindingRepository interface {
	// Save 创建绑定；重复绑定由唯一索引兜底：实现须刷新既有记录的
	// expires_at / granted_by 并保留原主键（过期后重绑必须重新生效）。
	// 调用方需要真实主键时通过 ListBySubject 回读。
	Save(ctx context.Context, binding *Binding) error
	// Delete 解绑；不存在也返回成功（幂等）。
	Delete(ctx context.Context, id string) error
	// FindByID 按主键加载绑定。
	FindByID(ctx context.Context, id string) (*Binding, error)
	// ListBySubject 返回主体在 at 时刻有效的绑定（过滤已过期）。
	ListBySubject(ctx context.Context, query SubjectQuery) ([]*Binding, error)
	// ListByRole 返回绑定到指定角色的全部绑定，用于删除前引用检查。
	ListByRole(ctx context.Context, roleID string) ([]*Binding, error)
}

// SubjectQuery 是主体查询条件，同时用于绑定列表与快照加载。
//
// MerchantID 在 B 端必填：空串不代表「全部商户」，
// 仓储实现必须对空值返回错误而不是放宽条件（SEC-14）。
type SubjectQuery struct {
	Channel     Channel
	SubjectType SubjectType
	SubjectID   string
	MerchantID  string
	// At 用于过滤过期绑定；零值表示当前时间。
	At time.Time
}

// EffectiveTime 返回用于过期判断的时间点，零值时取当前 UTC 时间。
func (q SubjectQuery) EffectiveTime() time.Time {
	if q.At.IsZero() {
		return nowUTC()
	}
	return q.At.UTC()
}

// ChangeRequestRepository 是审批单聚合的持久化端口。
//
// 约定：
//   - 未找到返回 internal/shared/apperr 的 NotFound；
//   - Save 必须使用 expectedVersion 做乐观锁，冲突返回 apperr 的 Conflict；
//   - 跨端 / 跨商户读取统一表现为 NotFound（SEC-14），调用方据此不得区分
//     「不存在」与「存在但不属于你」，避免用 404 / 403 的差异做枚举探测。
type ChangeRequestRepository interface {
	// Save 创建或更新审批单；expectedVersion 为 0 表示创建。
	Save(ctx context.Context, request *ChangeRequest, expectedVersion int64) error
	// FindByID 按主键加载审批单。
	FindByID(ctx context.Context, id string) (*ChangeRequest, error)
	// List 按条件分页列举审批单，按申请时间倒序。
	List(ctx context.Context, filter ChangeRequestFilter) ([]*ChangeRequest, error)
	// Count 返回与 List 相同条件的审批单总数，供分页 total 使用。
	Count(ctx context.Context, filter ChangeRequestFilter) (int, error)
}

// ChangeRequestFilter 是审批单列表过滤条件。
type ChangeRequestFilter struct {
	// Channel 端别过滤；B 端必填。
	Channel Channel
	// MerchantID 商户过滤；空表示平台级（A 端全量）。
	//
	// B 端必须按 Principal 的商户过滤，否则一次分页查询就会把其它商户的
	// 审批单（含权限载荷）暴露出去（SEC-14）。
	MerchantID string
	// Status 状态过滤；空表示全部。
	Status ChangeStatus
	// RequestedBy 申请人过滤；空表示全部。
	RequestedBy string
	// Action 动作过滤；空表示全部。
	Action ChangeAction
	// Limit 分页大小，上限 100（API-05）。
	Limit int
	// Offset 偏移量。
	Offset int
}

// SnapshotRepository 是鉴权快照的只读端口。
//
// 实现必须用一条 SQL 展开角色、权限与数据范围（禁止 N+1，§8.3）；
// 返回错误表示鉴权依赖不可用，调用方必须失败关闭（503 / 50300）。
type SnapshotRepository interface {
	Load(ctx context.Context, query SubjectQuery) (*SubjectSnapshot, error)
}

// SubjectSnapshot 是某主体在某一时刻的有效权限快照。
//
// 这是鉴权的唯一输入：JWT 只提供身份，权限一律以快照为准。
type SubjectSnapshot struct {
	Channel     Channel
	SubjectType SubjectType
	SubjectID   string
	MerchantID  string
	// Roles 生效角色编码（去重、排序），用于诊断与审计。
	Roles []string
	// Permissions 生效权限点并集（去重、排序）。
	Permissions []iam.PermissionCode
	// Breakglass 表示主体当前持有未过期的破玻璃绑定。
	//
	// 只有该标记为 true 时才允许 Permissions 中出现 "*"：普通角色永不含通配符，
	// 授权层据此在 prod 关闭 allow_wildcard 的前提下仍可放行破玻璃。
	Breakglass bool
	// Scopes 按资源合并后的数据范围（取并集，最宽者胜出）。
	Scopes []DataScope
	// Version 快照版本（可由角色最大版本或生成序号派生），用于缓存比对。
	Version int64
	// GeneratedAt 快照生成时间（UTC）。
	GeneratedAt time.Time
}

// HasPermission 判断快照是否包含指定权限码。
//
// "*" 只在 Breakglass 为 true 时生效，避免快照被注入通配符后无差别放行。
func (s *SubjectSnapshot) HasPermission(code iam.PermissionCode) bool {
	for _, candidate := range s.Permissions {
		if candidate == "*" && s.Breakglass {
			return true
		}
		if candidate == code {
			return true
		}
	}
	return false
}

// ScopeFor 返回指定资源的数据范围；ResourceAll 可作为兜底。
func (s *SubjectSnapshot) ScopeFor(resource string) (DataScope, bool) {
	for _, scope := range s.Scopes {
		if scope.Resource == resource {
			return scope, true
		}
	}
	for _, scope := range s.Scopes {
		if scope.Resource == ResourceAll {
			return scope, true
		}
	}
	return DataScope{}, false
}
