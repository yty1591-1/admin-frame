package domain

import (
	"slices"
	"strings"
	"time"
)

const (
	// MinSodGroupRoles 互斥组最少角色数；少于两个角色不构成互斥。
	MinSodGroupRoles = 2
	// MaxSodGroupRoles 单组角色上限，避免单次绑定校验拉取过多组成员。
	MaxSodGroupRoles = 64
	// MaxSodGroupCodeLen 组编码最大字节数，与 iam_sod_group.code 列宽一致。
	MaxSodGroupCodeLen = 64
	// MaxSodGroupNameLen 组显示名最大字节数。
	MaxSodGroupNameLen = 128
)

// SodGroupStatus 是互斥角色组生命周期状态。
type SodGroupStatus string

const (
	// SodGroupActive 组生效，绑定期执行互斥校验。
	SodGroupActive SodGroupStatus = "active"
	// SodGroupDisabled 组停用，只保留历史关系，不再拦截新绑定。
	SodGroupDisabled SodGroupStatus = "disabled"
)

// Validate 校验组状态取值。
func (s SodGroupStatus) Validate() error {
	switch s {
	case SodGroupActive, SodGroupDisabled:
		return nil
	default:
		return errInvalid(ErrInvalidSodGroup, "sod group status %q is not supported", string(s))
	}
}

// SodGroup 是 RBAC2 静态职责分离（SSD）聚合根。
//
// 不变量：
//   - 租户组合与角色一致：admin 端不带商户，biz 端必须带商户；
//   - 组内至少 2 个、至多 MaxSodGroupRoles 个角色，且不重复；
//   - MaxActiveRoles 取值 [1, MaxSodGroupRoles] 且必须小于组成员数，
//     否则该组不产生任何约束；
//   - 破玻璃角色不得加入互斥组；该判断依赖角色查询，由 app 层在写入前校验；
//   - 软删后不可复用 code，且不再参与绑定校验；
//   - 每次变更 version 递增，持久化必须带 expectedVersion 做乐观锁。
type SodGroup struct {
	// ID 雪花 ID。
	ID string
	// Channel 组所属端别。
	Channel Channel
	// MerchantID B 端商户 ID；A 端为空串。
	MerchantID string
	// Code 端内唯一的组编码；软删后不可复用。
	Code string
	// Name 显示名。
	Name string
	// Description 互斥的业务理由。
	Description string
	// MaxActiveRoles 同一主体在组内至多同时持有的角色数。
	MaxActiveRoles int
	// Status 生命周期状态。
	Status SodGroupStatus
	// Version 乐观锁版本，初始为 1。
	Version int64
	// CreatedBy / UpdatedBy 操作人 subject_id。
	CreatedBy string
	UpdatedBy string
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt 软删时间；nil 表示未删除。
	DeletedAt *time.Time
	// RoleIDs 组内角色 ID，永不为 nil。
	RoleIDs []string
}

// NewSodGroupParams 创建互斥组的入参。
type NewSodGroupParams struct {
	ID             string
	Channel        Channel
	MerchantID     string
	Code           string
	Name           string
	Description    string
	MaxActiveRoles int
	RoleIDs        []string
	Actor          string
}

// NewSodGroup 创建处于 active 状态、版本为 1 的互斥组。
func NewSodGroup(p NewSodGroupParams) (*SodGroup, error) {
	if p.ID == "" {
		return nil, errInvalid(ErrEmptyInput, "sod group id is required")
	}
	if p.Actor == "" {
		return nil, errInvalid(ErrMissingActor, "sod group creator is required")
	}
	if err := p.Channel.Validate(); err != nil {
		return nil, err
	}
	if err := validateSodTenant(p.Channel, p.MerchantID); err != nil {
		return nil, err
	}
	if err := ValidateSodGroupCode(p.Code); err != nil {
		return nil, err
	}
	if err := ValidateSodGroupName(p.Name); err != nil {
		return nil, err
	}
	if err := validateDescription(p.Description); err != nil {
		return nil, err
	}
	roleIDs, err := normalizeSodRoleIDs(p.RoleIDs)
	if err != nil {
		return nil, err
	}
	maxActive := p.MaxActiveRoles
	if maxActive == 0 {
		maxActive = 1
	}
	if err := validateSodMaxActive(maxActive, len(roleIDs)); err != nil {
		return nil, err
	}

	now := nowUTC()
	return &SodGroup{
		ID:             p.ID,
		Channel:        p.Channel,
		MerchantID:     p.MerchantID,
		Code:           p.Code,
		Name:           p.Name,
		Description:    p.Description,
		MaxActiveRoles: maxActive,
		Status:         SodGroupActive,
		Version:        1,
		CreatedBy:      p.Actor,
		UpdatedBy:      p.Actor,
		CreatedAt:      now,
		UpdatedAt:      now,
		RoleIDs:        roleIDs,
	}, nil
}

// ValidateSodGroupCode 校验组编码格式，规则与角色编码一致。
func ValidateSodGroupCode(code string) error {
	if code == "" {
		return errInvalid(ErrInvalidSodGroup, "sod group code is required")
	}
	if len(code) > MaxSodGroupCodeLen {
		return errInvalid(ErrInvalidSodGroup,
			"sod group code %q exceeds %d bytes", code, MaxSodGroupCodeLen)
	}
	if strings.ContainsRune(code, '*') {
		return errInvalid(ErrInvalidSodGroup, "sod group code %q must not contain wildcard *", code)
	}
	if !roleCodePattern.MatchString(code) {
		return errInvalid(ErrInvalidSodGroup,
			"sod group code %q must start with a lowercase letter and contain only [a-z0-9_], length 2-%d",
			code, MaxSodGroupCodeLen)
	}
	return nil
}

// ValidateSodGroupName 校验组显示名长度。
func ValidateSodGroupName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errInvalid(ErrInvalidSodGroup, "sod group name is required")
	}
	if len(name) > MaxSodGroupNameLen {
		return errInvalid(ErrInvalidSodGroup, "sod group name exceeds %d bytes", MaxSodGroupNameLen)
	}
	return nil
}

// validateSodTenant 校验租户组合：admin 端不得带商户，biz 端必须带商户。
func validateSodTenant(channel Channel, merchantID string) error {
	switch channel {
	case ChannelAdmin:
		if merchantID != "" {
			return errInvalid(ErrInvalidSodGroup, "admin sod group must not carry merchant id")
		}
	case ChannelBiz:
		if merchantID == "" {
			return errInvalid(ErrInvalidSodGroup, "biz sod group requires merchant id")
		}
	default:
		return errInvalid(ErrInvalidSodGroup, "sod group channel %q is not supported", string(channel))
	}
	return nil
}

// normalizeSodRoleIDs 去重校验并克隆组内角色 ID：拒绝空值、重复与数量越界。
func normalizeSodRoleIDs(roleIDs []string) ([]string, error) {
	if len(roleIDs) < MinSodGroupRoles {
		return nil, errInvalid(ErrInvalidSodGroup,
			"sod group requires at least %d roles", MinSodGroupRoles)
	}
	if len(roleIDs) > MaxSodGroupRoles {
		return nil, errInvalid(ErrInvalidSodGroup,
			"sod group role count %d exceeds %d", len(roleIDs), MaxSodGroupRoles)
	}
	seen := make(map[string]struct{}, len(roleIDs))
	out := make([]string, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		if strings.TrimSpace(roleID) == "" {
			return nil, errInvalid(ErrInvalidSodGroup, "sod group role id is required")
		}
		if _, dup := seen[roleID]; dup {
			return nil, errInvalid(ErrInvalidSodGroup, "duplicate role id %q in sod group", roleID)
		}
		seen[roleID] = struct{}{}
		out = append(out, roleID)
	}
	return out, nil
}

// validateSodMaxActive 校验并发阈值：必须小于组成员数，否则组没有约束力。
func validateSodMaxActive(maxActive, roleCount int) error {
	if maxActive < 1 || maxActive > MaxSodGroupRoles {
		return errInvalid(ErrInvalidSodGroup,
			"max_active_roles %d must be in [1, %d]", maxActive, MaxSodGroupRoles)
	}
	if maxActive >= roleCount {
		return errInvalid(ErrInvalidSodGroup,
			"max_active_roles %d must be less than role count %d", maxActive, roleCount)
	}
	return nil
}

// IsDeleted 报告互斥组是否已软删。
func (g *SodGroup) IsDeleted() bool { return g.DeletedAt != nil }

// IsActive 报告互斥组是否会拦截新绑定。
func (g *SodGroup) IsActive() bool {
	return g.Status == SodGroupActive && !g.IsDeleted()
}

// SortedRoleIDs 返回确定性顺序的组内角色 ID，供持久化与审计。
func (g *SodGroup) SortedRoleIDs() []string {
	out := slices.Clone(g.RoleIDs)
	slices.Sort(out)
	return out
}

// UpdateDetails 更新显示名、说明与并发阈值。
func (g *SodGroup) UpdateDetails(name, description string, maxActiveRoles int, actor string, expectedVersion int64) error {
	if err := g.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if err := ValidateSodGroupName(name); err != nil {
		return err
	}
	if err := validateDescription(description); err != nil {
		return err
	}
	if maxActiveRoles == 0 {
		maxActiveRoles = 1
	}
	if err := validateSodMaxActive(maxActiveRoles, len(g.RoleIDs)); err != nil {
		return err
	}
	g.Name = strings.TrimSpace(name)
	g.Description = description
	g.MaxActiveRoles = maxActiveRoles
	g.touch(actor)
	return nil
}

// ReplaceRoles 全量替换组内角色；并发阈值必须仍小于新成员数。
func (g *SodGroup) ReplaceRoles(roleIDs []string, actor string, expectedVersion int64) error {
	if err := g.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	next, err := normalizeSodRoleIDs(roleIDs)
	if err != nil {
		return err
	}
	if err := validateSodMaxActive(g.MaxActiveRoles, len(next)); err != nil {
		return err
	}
	g.RoleIDs = next
	g.touch(actor)
	return nil
}

// Disable 停用互斥组；已停用时幂等返回（不递增版本）。
func (g *SodGroup) Disable(actor string, expectedVersion int64) error {
	if err := g.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if g.Status == SodGroupDisabled {
		return nil
	}
	g.Status = SodGroupDisabled
	g.touch(actor)
	return nil
}

// Enable 启用互斥组；已启用时幂等返回（不递增版本）。
func (g *SodGroup) Enable(actor string, expectedVersion int64) error {
	if err := g.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	if g.Status == SodGroupActive {
		return nil
	}
	g.Status = SodGroupActive
	g.touch(actor)
	return nil
}

// Delete 软删互斥组，保留 code 防止复用，并置为 disabled。
func (g *SodGroup) Delete(actor string, expectedVersion int64) error {
	if g.IsDeleted() {
		return errConflict(ErrInvalidSodGroup, "sod group %s is already deleted", g.ID)
	}
	if err := g.ensureMutable(actor, expectedVersion); err != nil {
		return err
	}
	now := nowUTC()
	g.DeletedAt = &now
	g.Status = SodGroupDisabled
	g.touch(actor)
	return nil
}

// ensureMutable 校验未删除、操作人存在与乐观锁版本。
func (g *SodGroup) ensureMutable(actor string, expectedVersion int64) error {
	if g.IsDeleted() {
		return errConflict(ErrInvalidSodGroup, "sod group %s is deleted", g.ID)
	}
	if actor == "" {
		return errInvalid(ErrMissingActor, "actor is required for sod group %s", g.ID)
	}
	if expectedVersion != g.Version {
		return errConflict(ErrVersionConflict,
			"sod group %s version %d does not match expected %d; reload and retry",
			g.ID, g.Version, expectedVersion)
	}
	return nil
}

// touch 更新审计字段并递增版本。
func (g *SodGroup) touch(actor string) {
	now := nowUTC()
	g.UpdatedBy = actor
	g.UpdatedAt = now
	g.Version++
}

// SodViolation 描述一个主体在互斥组内的有效角色数超过阈值。
//
// 配置期（创建 / 改组 / 启用）用它做存量预检：任一违规都意味着新规则会让
// 既有主体立即处于「违规持有」状态，必须在写入前拒绝并让运维先清理。
type SodViolation struct {
	// Channel / SubjectType / SubjectID / MerchantID 定位违规主体。
	Channel     Channel
	SubjectType SubjectType
	SubjectID   string
	MerchantID  string
	// RoleIDs 该主体在组内实际持有的角色（含继承）。
	RoleIDs []string
}
