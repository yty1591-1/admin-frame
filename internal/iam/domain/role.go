package domain

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
)

const (
	// MaxRoleCodeLen 角色编码最大字节数，与 iam_role.code 列宽一致。
	MaxRoleCodeLen = 64
	// MaxRoleNameLen 角色显示名最大字节数。
	MaxRoleNameLen = 128
	// MaxRoleDescriptionLen 角色说明最大字节数。
	MaxRoleDescriptionLen = 512
)

// roleCodePattern 约束角色编码：小写字母开头，仅小写字母、数字、下划线。
//
// 不允许 "-" / 大写：角色编码会出现在 URL 路径与审计事件中，保持单一规范可避免
// 大小写不敏感排序规则（utf8mb4_0900_ai_ci）下的唯一索引歧义。
var roleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// Role 是角色聚合根。
//
// 不变量：
//   - platform 角色必须属于 admin 端且 merchant_id 为空；
//   - merchant 角色必须属于 biz 端且 merchant_id 非空；
//   - 权限集合不含通配符；数据范围类型与端别相容；
//   - 软删后不可复用 code，且不再参与鉴权快照；
//   - 每次变更 version 递增，持久化必须带 expectedVersion 做乐观锁。
type Role struct {
	// ID 雪花 ID。
	ID string
	// Channel 角色所属端别。
	Channel Channel
	// Scope 角色作用域。
	Scope RoleScopeType
	// MerchantID B 端商户 ID；platform 角色为空串。
	MerchantID string
	// Code 端内唯一的角色编码；软删后不可复用。
	Code string
	// Name 显示名。
	Name string
	// Description 职责说明。
	Description string
	// Type 角色类型。
	Type RoleType
	// Status 生命周期状态。
	Status RoleStatus
	// Version 乐观锁版本，初始为 1。
	Version int64
	// CreatedBy / UpdatedBy 操作人 subject_id，用于审计。
	CreatedBy string
	UpdatedBy string
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt 软删时间；nil 表示未删除。
	DeletedAt *time.Time
	// Permissions 已授予权限集合，永不为 nil。
	Permissions PermissionSet
	// DataScopes 数据范围，按 resource 索引。
	DataScopes map[string]DataScope
}

// NewRoleParams 创建角色的入参。
type NewRoleParams struct {
	ID          string
	Channel     Channel
	Scope       RoleScopeType
	MerchantID  string
	Code        string
	Name        string
	Description string
	Type        RoleType
	Actor       string
}

// NewRole 创建处于 active 状态、版本为 1 的角色。
//
// 破玻璃角色不能直接创建：必须经审批单（M4）落地，避免绕过双人审批拿到超级权限。
func NewRole(p NewRoleParams) (*Role, error) {
	if p.ID == "" {
		return nil, errInvalid(ErrEmptyInput, "role id is required")
	}
	if p.Actor == "" {
		return nil, errInvalid(ErrMissingActor, "role creator is required")
	}
	if err := p.Channel.Validate(); err != nil {
		return nil, err
	}
	if err := p.Scope.Validate(); err != nil {
		return nil, err
	}
	if err := validateScopeMerchant(p.Channel, p.Scope, p.MerchantID); err != nil {
		return nil, err
	}
	if err := ValidateRoleCode(p.Code); err != nil {
		return nil, err
	}
	if err := ValidateRoleName(p.Name); err != nil {
		return nil, err
	}
	if err := validateDescription(p.Description); err != nil {
		return nil, err
	}

	roleType := p.Type
	if roleType == "" {
		roleType = RoleTypeCustom
	}
	if err := roleType.Validate(); err != nil {
		return nil, err
	}
	if roleType == RoleTypeBreakglass {
		return nil, errForbidden(ErrBreakglassRequiresApproval,
			"breakglass role must be created through the approval workflow")
	}

	now := nowUTC()
	return &Role{
		ID:          p.ID,
		Channel:     p.Channel,
		Scope:       p.Scope,
		MerchantID:  p.MerchantID,
		Code:        p.Code,
		Name:        p.Name,
		Description: p.Description,
		Type:        roleType,
		Status:      RoleStatusActive,
		Version:     1,
		CreatedBy:   p.Actor,
		UpdatedBy:   p.Actor,
		CreatedAt:   now,
		UpdatedAt:   now,
		Permissions: make(PermissionSet),
		DataScopes:  make(map[string]DataScope),
	}, nil
}

// Initialize 在首次落库前设置权限与数据范围，保持 version = 1。
//
// 创建路径不该模拟一次「更新」：新建角色尚未对外暴露任何版本，若复用
// ReplacePermissions / ReplaceDataScopes 会让创建响应出现 version=3 这类无意义值。
// 已落库角色必须走 Replace* 方法，保留乐观锁语义。
func (r *Role) Initialize(permissions PermissionSet, scopes []DataScope) error {
	if r.Version != 1 {
		return errConflict(ErrVersionConflict,
			"role %s is already persisted (version %d); use Replace* methods", r.ID, r.Version)
	}
	if permissions == nil {
		permissions = make(PermissionSet)
	}
	for _, code := range permissions.Sorted() {
		if err := ValidatePermissionCode(code); err != nil {
			return err
		}
	}
	next := make(map[string]DataScope, len(scopes))
	for _, scope := range scopes {
		if err := scope.Validate(r.Channel); err != nil {
			return err
		}
		if _, dup := next[scope.Resource]; dup {
			return errInvalid(ErrInvalidDataScope,
				"duplicate data scope for resource %q", scope.Resource)
		}
		scope.MerchantIDs = slices.Clone(scope.MerchantIDs)
		next[scope.Resource] = scope
	}
	r.Permissions = permissions.Clone()
	r.DataScopes = next
	return nil
}

// ValidateRoleCode 校验角色编码格式。
func ValidateRoleCode(code string) error {
	if code == "" {
		return errInvalid(ErrInvalidRoleCode, "role code is required")
	}
	if len(code) > MaxRoleCodeLen {
		return errInvalid(ErrInvalidRoleCode, "role code %q exceeds %d bytes", code, MaxRoleCodeLen)
	}
	if strings.ContainsRune(code, '*') {
		return errInvalid(ErrWildcardNotAllowed, "role code %q must not contain wildcard *", code)
	}
	if !roleCodePattern.MatchString(code) {
		return errInvalid(ErrInvalidRoleCode,
			"role code %q must start with a lowercase letter and contain only [a-z0-9_], length 2-%d",
			code, MaxRoleCodeLen)
	}
	return nil
}

// ValidateRoleName 校验角色显示名长度。
func ValidateRoleName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errInvalid(ErrEmptyInput, "role name is required")
	}
	if len(name) > MaxRoleNameLen {
		return errInvalid(ErrEmptyInput, "role name exceeds %d bytes", MaxRoleNameLen)
	}
	return nil
}

// validateDescription 校验角色说明长度（可为空）。
func validateDescription(description string) error {
	if len(description) > MaxRoleDescriptionLen {
		return errInvalid(ErrEmptyInput, "role description exceeds %d bytes", MaxRoleDescriptionLen)
	}
	return nil
}

// validateScopeMerchant 校验作用域与端别 / 商户的一致性。
func validateScopeMerchant(channel Channel, scope RoleScopeType, merchantID string) error {
	switch scope {
	case RoleScopePlatform:
		if channel != ChannelAdmin {
			return errInvalid(ErrInvalidRoleScope, "platform role requires channel admin")
		}
		if merchantID != "" {
			return errInvalid(ErrInvalidRoleScope, "platform role must not carry merchant id")
		}
	case RoleScopeMerchant:
		if channel != ChannelBiz {
			return errInvalid(ErrInvalidRoleScope, "merchant role requires channel biz")
		}
		if merchantID == "" {
			return errInvalid(ErrInvalidRoleScope, "merchant role requires merchant id")
		}
	default:
		return errInvalid(ErrInvalidRoleScope, "role scope %q must be platform or merchant", string(scope))
	}
	return nil
}

// IsDeleted 报告角色是否已软删。
func (r *Role) IsDeleted() bool { return r.DeletedAt != nil }

// IsActive 报告角色是否可用于鉴权（未删除且状态为 active）。
func (r *Role) IsActive() bool {
	return r.Status == RoleStatusActive && !r.IsDeleted()
}

// HasPermission 判断角色是否已授予某权限码。
func (r *Role) HasPermission(code iam.PermissionCode) bool {
	return r.Permissions.Contains(code)
}

// PermissionCodes 返回已授予权限码的确定性顺序。
func (r *Role) PermissionCodes() []iam.PermissionCode {
	return r.Permissions.Sorted()
}

// DataScopeFor 返回指定资源的数据范围。
func (r *Role) DataScopeFor(resource string) (DataScope, bool) {
	scope, ok := r.DataScopes[resource]
	return scope, ok
}

// SortedDataScopes 返回按资源排序的数据范围，供持久化与审计。
func (r *Role) SortedDataScopes() []DataScope {
	out := make([]DataScope, 0, len(r.DataScopes))
	for _, scope := range r.DataScopes {
		out = append(out, scope)
	}
	slices.SortFunc(out, func(a, b DataScope) int { return strings.Compare(a.Resource, b.Resource) })
	return out
}

// MissingDataScopes 返回已授予权限中缺少数据范围配置的资源列表。
//
// 参数 required 由 app 层从权限目录中筛出 data_bearing 的资源段传入；
// 领域层不感知目录，只负责结构一致性检查，避免目录与领域互相耦合。
func (r *Role) MissingDataScopes(required []string) []string {
	missing := make([]string, 0, len(required))
	seen := make(map[string]struct{}, len(required))
	for _, resource := range required {
		if resource == "" {
			continue
		}
		if _, dup := seen[resource]; dup {
			continue
		}
		seen[resource] = struct{}{}
		if _, ok := r.DataScopes[resource]; !ok {
			if _, all := r.DataScopes[ResourceAll]; all {
				continue
			}
			missing = append(missing, resource)
		}
	}
	slices.Sort(missing)
	return missing
}

// UpdateDetails 更新显示名与说明。
func (r *Role) UpdateDetails(name, description, actor string, expectedVersion int64) error {
	if err := r.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if err := ValidateRoleName(name); err != nil {
		return err
	}
	if err := validateDescription(description); err != nil {
		return err
	}
	r.Name = strings.TrimSpace(name)
	r.Description = description
	r.touch(actor)
	return nil
}

// Disable 停用角色；已停用时幂等返回（不递增版本）。
func (r *Role) Disable(actor string, expectedVersion int64) error {
	if err := r.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if r.Status == RoleStatusDisabled {
		return nil
	}
	r.Status = RoleStatusDisabled
	r.touch(actor)
	return nil
}

// Enable 重新启用角色；已启用时幂等返回（不递增版本）。
func (r *Role) Enable(actor string, expectedVersion int64) error {
	if err := r.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if r.Status == RoleStatusActive {
		return nil
	}
	r.Status = RoleStatusActive
	r.touch(actor)
	return nil
}

// Delete 软删角色：置为 disabled 并记录删除时间，保留 code 防止重建洗白。
func (r *Role) Delete(actor string, expectedVersion int64) error {
	if r.IsDeleted() {
		return errConflict(ErrRoleDeleted, "role %s is already deleted", r.ID)
	}
	if err := r.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	now := nowUTC()
	r.DeletedAt = &now
	r.Status = RoleStatusDisabled
	r.touch(actor)
	return nil
}

// ReplacePermissions 全量替换角色权限，返回差量供审计。
//
// 停用角色不允许授权：否则「先授权、后启用」会绕过审批链，且快照不加载其权限，
// 造成授权成功但鉴权失败的错觉。
func (r *Role) ReplacePermissions(next PermissionSet, actor string, expectedVersion int64) (PermissionChange, error) {
	if err := r.ensureMutable(actor, expectedVersion); err != nil {
		return PermissionChange{}, err
	}
	if r.Status != RoleStatusActive {
		return PermissionChange{}, errConflict(ErrRoleNotActive,
			"role %s is disabled; enable it before granting permissions", r.ID)
	}
	if next == nil {
		next = make(PermissionSet)
	}
	// 即使调用方绕过 NewPermissionSet，也在聚合内复核一次，确保通配符永不落库。
	for _, code := range next.Sorted() {
		if err := ValidatePermissionCode(code); err != nil {
			return PermissionChange{}, err
		}
	}
	change := r.Permissions.Diff(next)
	if change.IsEmpty() {
		return change, nil
	}
	r.Permissions = next.Clone()
	r.touch(actor)
	return change, nil
}

// ReplaceDataScopes 全量替换数据范围；按资源去重，重复资源直接拒绝。
func (r *Role) ReplaceDataScopes(scopes []DataScope, actor string, expectedVersion int64) error {
	if err := r.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	next := make(map[string]DataScope, len(scopes))
	for _, scope := range scopes {
		if err := scope.Validate(r.Channel); err != nil {
			return err
		}
		if _, dup := next[scope.Resource]; dup {
			return errInvalid(ErrInvalidDataScope,
				"duplicate data scope for resource %q", scope.Resource)
		}
		scope.MerchantIDs = slices.Clone(scope.MerchantIDs)
		next[scope.Resource] = scope
	}
	r.DataScopes = next
	r.touch(actor)
	return nil
}

// ensureMutable 校验写操作前置条件：未删除、操作人存在、版本匹配。
func (r *Role) ensureMutable(actor string, expectedVersion int64) error {
	if r.IsDeleted() {
		return errConflict(ErrRoleDeleted, "role %s is deleted", r.ID)
	}
	if actor == "" {
		return errInvalid(ErrMissingActor, "actor is required for role %s", r.ID)
	}
	if expectedVersion != r.Version {
		return errConflict(ErrVersionConflict,
			"role %s version %d does not match expected %d; reload and retry",
			r.ID, r.Version, expectedVersion)
	}
	return nil
}

// touch 更新审计字段并递增版本。
func (r *Role) touch(actor string) {
	now := nowUTC()
	r.UpdatedBy = actor
	r.UpdatedAt = now
	r.Version++
}

// nowUTC 返回 UTC 当前时间；集中一处便于测试与将来注入时钟。
func nowUTC() time.Time { return time.Now().UTC() }
