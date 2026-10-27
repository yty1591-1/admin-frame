package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/iam"
)

// RoleTemplateStatus 是角色模板生命周期状态。
type RoleTemplateStatus string

const (
	// RoleTemplateActive 可实例化。
	RoleTemplateActive RoleTemplateStatus = "active"
	// RoleTemplateDisabled 停止新实例化；既有实例保留。
	RoleTemplateDisabled RoleTemplateStatus = "disabled"
)

// Validate 校验模板状态取值。
func (s RoleTemplateStatus) Validate() error {
	switch s {
	case RoleTemplateActive, RoleTemplateDisabled:
		return nil
	default:
		return errInvalid(ErrInvalidRoleTemplate, "role template status %q is not supported", string(s))
	}
}

// RoleTemplate 是角色模板聚合根：平台定义权限与数据范围基线，商户按需实例化。
//
// 实例与模板是复制关系而非继承：实例化时复制权限与数据范围，模板后续变更
// 不会自动扩散到存量角色，必须显式同步，避免平台改动无通知地扩大商户权限。
//
// 不变量：
//
//   - 编码格式与角色一致，端内唯一，软删后不可复用；
//   - 模板只承载 admin / biz 端别；数据范围类型须与端别相容；
//   - 版本在权限或数据范围变更时递增，用于识别实例漂移；
//   - 停用模板不允许新实例化，但不影响既有实例。
type RoleTemplate struct {
	// ID 雪花 ID。
	ID string
	// Channel 适用端别。
	Channel Channel
	// Code 端内唯一编码。
	Code string
	// Name 显示名。
	Name string
	// Description 说明与适用场景。
	Description string
	// Status 生命周期状态。
	Status RoleTemplateStatus
	// Version 模板版本，初始为 1。
	Version int64
	// CreatedBy / UpdatedBy 操作人 subject_id。
	CreatedBy string
	UpdatedBy string
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt 软删时间；nil 表示未删除。
	DeletedAt *time.Time
	// Permissions 模板权限基线，永不为 nil。
	Permissions PermissionSet
	// DataScopes 模板数据范围基线，按 resource 索引。
	DataScopes map[string]DataScope
}

// NewRoleTemplateParams 创建模板的入参。
type NewRoleTemplateParams struct {
	ID          string
	Channel     Channel
	Code        string
	Name        string
	Description string
	Actor       string
}

// NewRoleTemplate 创建处于 active 状态、版本为 1 的模板。
func NewRoleTemplate(p NewRoleTemplateParams) (*RoleTemplate, error) {
	if p.ID == "" {
		return nil, errInvalid(ErrEmptyInput, "role template id is required")
	}
	if p.Actor == "" {
		return nil, errInvalid(ErrMissingActor, "role template creator is required")
	}
	if err := p.Channel.Validate(); err != nil {
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
	now := nowUTC()
	return &RoleTemplate{
		ID:          p.ID,
		Channel:     p.Channel,
		Code:        p.Code,
		Name:        p.Name,
		Description: p.Description,
		Status:      RoleTemplateActive,
		Version:     1,
		CreatedBy:   p.Actor,
		UpdatedBy:   p.Actor,
		CreatedAt:   now,
		UpdatedAt:   now,
		Permissions: make(PermissionSet),
		DataScopes:  make(map[string]DataScope),
	}, nil
}

// IsDeleted 报告模板是否已软删。
func (t *RoleTemplate) IsDeleted() bool { return t.DeletedAt != nil }

// IsActive 报告模板是否可用于实例化。
func (t *RoleTemplate) IsActive() bool {
	return t.Status == RoleTemplateActive && !t.IsDeleted()
}

// PermissionCodes 返回模板权限码的确定性顺序。
func (t *RoleTemplate) PermissionCodes() []iam.PermissionCode { return t.Permissions.Sorted() }

// SortedDataScopes 返回按资源排序的数据范围。
func (t *RoleTemplate) SortedDataScopes() []DataScope {
	out := make([]DataScope, 0, len(t.DataScopes))
	for _, scope := range t.DataScopes {
		out = append(out, scope)
	}
	slices.SortFunc(out, func(a, b DataScope) int { return strings.Compare(a.Resource, b.Resource) })
	return out
}

// UpdateDetails 更新显示名与说明。
func (t *RoleTemplate) UpdateDetails(name, description, actor string, expectedVersion int64) error {
	if err := t.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if err := ValidateRoleName(name); err != nil {
		return err
	}
	if err := validateDescription(description); err != nil {
		return err
	}
	t.Name = strings.TrimSpace(name)
	t.Description = description
	t.touch(actor)
	return nil
}

// ReplacePermissions 全量替换模板权限，版本递增。
func (t *RoleTemplate) ReplacePermissions(next PermissionSet, actor string, expectedVersion int64) error {
	if err := t.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if next == nil {
		next = make(PermissionSet)
	}
	for _, code := range next.Sorted() {
		if err := ValidatePermissionCode(code); err != nil {
			return err
		}
	}
	t.Permissions = next.Clone()
	t.touch(actor)
	return nil
}

// ReplaceDataScopes 全量替换模板数据范围，版本递增。
func (t *RoleTemplate) ReplaceDataScopes(scopes []DataScope, actor string, expectedVersion int64) error {
	if err := t.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	next := make(map[string]DataScope, len(scopes))
	for _, scope := range scopes {
		if err := scope.Validate(t.Channel); err != nil {
			return err
		}
		if _, dup := next[scope.Resource]; dup {
			return errInvalid(ErrInvalidDataScope,
				"duplicate data scope for resource %q", scope.Resource)
		}
		scope.MerchantIDs = slices.Clone(scope.MerchantIDs)
		next[scope.Resource] = scope
	}
	t.DataScopes = next
	t.touch(actor)
	return nil
}

// Disable 停用模板；已停用时幂等返回（不递增版本）。
func (t *RoleTemplate) Disable(actor string, expectedVersion int64) error {
	if err := t.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if t.Status == RoleTemplateDisabled {
		return nil
	}
	t.Status = RoleTemplateDisabled
	t.touch(actor)
	return nil
}

// Enable 重新启用模板；已启用时幂等返回（不递增版本）。
func (t *RoleTemplate) Enable(actor string, expectedVersion int64) error {
	if err := t.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if t.Status == RoleTemplateActive {
		return nil
	}
	t.Status = RoleTemplateActive
	t.touch(actor)
	return nil
}

// Delete 软删模板，保留 code 防止复用。
func (t *RoleTemplate) Delete(actor string, expectedVersion int64) error {
	if t.IsDeleted() {
		return errConflict(ErrInvalidRoleTemplate,
			"role template %s is already deleted", t.ID)
	}
	if err := t.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	now := nowUTC()
	t.DeletedAt = &now
	t.Status = RoleTemplateDisabled
	t.touch(actor)
	return nil
}

// ensureMutable 校验未删除、操作人存在与乐观锁版本。
func (t *RoleTemplate) ensureMutable(actor string, expectedVersion int64) error {
	if t.IsDeleted() {
		return errConflict(ErrInvalidRoleTemplate,
			"role template %s is deleted", t.ID)
	}
	if actor == "" {
		return errInvalid(ErrMissingActor, "actor is required for role template %s", t.ID)
	}
	if expectedVersion != t.Version {
		return errConflict(ErrVersionConflict,
			"role template %s version %d does not match expected %d; reload and retry",
			t.ID, t.Version, expectedVersion)
	}
	return nil
}

// touch 更新审计字段并递增版本。
func (t *RoleTemplate) touch(actor string) {
	now := nowUTC()
	t.UpdatedBy = actor
	t.UpdatedAt = now
	t.Version++
}
