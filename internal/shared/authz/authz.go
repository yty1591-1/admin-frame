// Package authz 定义鉴权快照的共享契约类型与请求上下文传递方式。
//
// 本包只承载纯类型与 context 辅助函数，不放缓存、数据库或业务判定：
// 快照由 internal/iam/app 实现 security.SnapshotLoader 加载，由 AuthZ 中间件
// 写入请求 context，业务 app 再读取 authz.Snapshot.Scopes 落实数据范围
// （docs/rbac-iam.md §6.3 / §8.2）。
//
// 依赖方向：authz 只依赖标准库；不得 import 任何业务域。
package authz

import (
	"context"
	"time"
)

// 数据范围类型；字面值与 internal/iam/domain.DataScopeType 保持一致，
// 序列化到缓存 / 跨进程契约时不得改名。
const (
	// ScopeAll 表示租户内全部数据（仅 A 端）。
	ScopeAll = "all"
	// ScopeMerchant 表示当前商户数据。
	ScopeMerchant = "merchant"
	// ScopeSelf 表示仅本人创建的数据。
	ScopeSelf = "self"
	// ScopeMerchantSet 表示显式枚举的商户集合（仅 A 端）。
	ScopeMerchantSet = "merchant_set"
)

// Scope 是某一资源的数据范围；Type 取 ScopeAll / ScopeMerchant / ScopeSelf /
// ScopeMerchantSet，MerchantIDs 仅在 Type 为 ScopeMerchantSet 时有值。
type Scope struct {
	Type        string   `json:"type"`
	MerchantIDs []string `json:"merchant_ids,omitempty"`
}

// Clone 返回数据范围的深拷贝，避免调用方修改缓存中的共享切片。
func (s Scope) Clone() Scope {
	out := s
	out.MerchantIDs = append([]string(nil), s.MerchantIDs...)
	return out
}

// Snapshot 是某一时刻主体的有效权限快照，也是鉴权的唯一输入。
//
// 约定：
//   - JWT 中的 roles / permissions 一律不信任，只以本快照为准；
//   - 缓存序列化格式须保持向后兼容，schema 版本由基础设施层维护；
//   - 快照一经写入 context 即视为只读，修改请先 Clone。
type Snapshot struct {
	// Channel 端别（admin / biz）。
	Channel string `json:"channel"`
	// SubjectType 主体类型（staff / merchant_staff）。
	SubjectType string `json:"subject_type"`
	// SubjectID 主体唯一标识。
	SubjectID string `json:"subject_id"`
	// MerchantID 商户标识；A 端为空。
	MerchantID string `json:"merchant_id"`
	// Roles 生效角色编码（去重、排序），仅用于诊断与审计。
	Roles []string `json:"roles"`
	// Permissions 生效权限点并集（去重、排序）。
	Permissions []string `json:"permissions"`
	// Scopes 按资源合并后的数据范围（resource -> scope）。
	Scopes map[string]Scope `json:"scopes,omitempty"`
	// Version 快照版本（角色最大版本），用于缓存比对与排障。
	Version int64 `json:"version"`
	// GeneratedAt 快照生成时间（UTC）。
	GeneratedAt time.Time `json:"generated_at"`
}

// Clone 返回快照的深拷贝，切片与映射均与源快照隔离。
func (s Snapshot) Clone() Snapshot {
	out := s
	out.Roles = append([]string(nil), s.Roles...)
	out.Permissions = append([]string(nil), s.Permissions...)
	if s.Scopes != nil {
		out.Scopes = make(map[string]Scope, len(s.Scopes))
		for resource, scope := range s.Scopes {
			out.Scopes[resource] = scope.Clone()
		}
	}
	return out
}

// Empty 表示主体没有任何有效角色与权限（空快照按默认拒绝处理）。
//
// 空快照仍应写入短 TTL 缓存（negative_ttl），避免异常主体持续回源。
func (s Snapshot) Empty() bool {
	return len(s.Roles) == 0 && len(s.Permissions) == 0
}

// RoleCodeSystemAdmin 是引导用超级管理员角色编码（migrations/000005 system_admin）。
const RoleCodeSystemAdmin = "system_admin"

// HasPermission 判断快照是否包含指定权限码。
//
// 支持已展开的 "*"：它只会由破玻璃绑定在有效期内产生，普通角色永不携带。
func (s Snapshot) HasPermission(code string) bool {
	for _, candidate := range s.Permissions {
		if candidate == code || candidate == "*" {
			return true
		}
	}
	return false
}

// HasRole 判断快照是否包含指定角色编码（精确匹配，大小写敏感）。
func (s Snapshot) HasRole(code string) bool {
	if code == "" {
		return false
	}
	for _, role := range s.Roles {
		if role == code {
			return true
		}
	}
	return false
}

// IsSystemAdmin 判断快照是否持有超级管理员角色。
func (s Snapshot) IsSystemAdmin() bool {
	return s.HasRole(RoleCodeSystemAdmin)
}

// HasRole 从请求 context 读取快照并判断角色；无快照时返回 false。
func HasRole(ctx context.Context, code string) bool {
	snapshot, ok := FromContext(ctx)
	if !ok {
		return false
	}
	return snapshot.HasRole(code)
}

// IsSystemAdmin 从请求 context 判断是否为超级管理员；无快照时返回 false。
//
// 用于高风险直写放行与 SoD 自批旁路；调用方须另行写入 sod_bypass 审计。
func IsSystemAdmin(ctx context.Context) bool {
	return HasRole(ctx, RoleCodeSystemAdmin)
}

// ScopeFor 返回指定资源的数据范围；资源未配置时回退到 ScopeAll 键。
func (s Snapshot) ScopeFor(resource string) (Scope, bool) {
	if scope, ok := s.Scopes[resource]; ok {
		return scope.Clone(), true
	}
	scope, ok := s.Scopes["*"]
	return scope.Clone(), ok
}

type snapshotKey struct{}

// WithSnapshot 将快照写入 context；仅由 AuthZ 中间件或 app 加载器调用。
func WithSnapshot(ctx context.Context, s Snapshot) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, snapshotKey{}, s)
}

// FromContext 读取当前请求的快照；不存在时 ok 为 false。
//
// 返回的快照与 context 中的值共享底层切片，调用方不得修改；需要修改时先 Clone。
func FromContext(ctx context.Context) (Snapshot, bool) {
	if ctx == nil {
		return Snapshot{}, false
	}
	s, ok := ctx.Value(snapshotKey{}).(Snapshot)
	return s, ok
}
