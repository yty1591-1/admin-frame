package domain

import (
	"fmt"
	"strings"
	"time"
)

// MemberRole 是租户成员角色。
type MemberRole string

// 成员角色取值。
//
// 刻意只有三级：owner 管成员与租户自身，admin 管业务配置，member 只读业务。
// 更细的权限交给 IAM（角色绑定），租户成员表不复制权限模型（计划 §3.1 禁止事项）。
const (
	// MemberRoleOwner 租户所有者：可管理成员，且每个租户至少保留一名。
	MemberRoleOwner MemberRole = "owner"
	// MemberRoleAdmin 租户管理员。
	MemberRoleAdmin MemberRole = "admin"
	// MemberRoleMember 普通成员。
	MemberRoleMember MemberRole = "member"
)

// Valid 判断角色是否已定义。
func (r MemberRole) Valid() bool {
	switch r {
	case MemberRoleOwner, MemberRoleAdmin, MemberRoleMember:
		return true
	default:
		return false
	}
}

// MemberStatus 是成员状态。
type MemberStatus string

// 成员状态取值。
const (
	// MemberStatusActive 在册。
	MemberStatusActive MemberStatus = "active"
	// MemberStatusDisabled 已停用：保留「曾在过该租户」的事实，不物理删除。
	MemberStatusDisabled MemberStatus = "disabled"
)

// Valid 判断状态是否已定义。
func (s MemberStatus) Valid() bool {
	return s == MemberStatusActive || s == MemberStatusDisabled
}

// MaxSubjectIDLength 主体 ID 长度上限（与 tenant_member.subject_id 对齐）。
const MaxSubjectIDLength = 64

// Member 是租户成员。
type Member struct {
	// TenantID 所属租户。
	TenantID string
	// SubjectID 成员主体 ID。
	SubjectID string
	// Role 成员角色。
	Role MemberRole
	// Status 成员状态。
	Status MemberStatus
	// CreatedAt 首次加入时间。
	CreatedAt time.Time
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// NewMember 构造在册成员。
func NewMember(tenantID, subjectID string, role MemberRole, now time.Time) (Member, error) {
	trimmedTenant := strings.TrimSpace(tenantID)
	if trimmedTenant == "" {
		return Member{}, fmt.Errorf("tenant: member tenant id is required")
	}
	trimmedSubject := strings.TrimSpace(subjectID)
	if trimmedSubject == "" {
		return Member{}, ErrSubjectRequired
	}
	if len([]rune(trimmedSubject)) > MaxSubjectIDLength {
		return Member{}, ErrSubjectTooLong
	}
	if !role.Valid() {
		return Member{}, &InvalidRoleError{Role: string(role)}
	}
	return Member{
		TenantID:  trimmedTenant,
		SubjectID: trimmedSubject,
		Role:      role,
		Status:    MemberStatusActive,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

// Validate 校验成员实体自洽性。
func (m Member) Validate() error {
	if strings.TrimSpace(m.TenantID) == "" {
		return fmt.Errorf("tenant: member tenant id is required")
	}
	if strings.TrimSpace(m.SubjectID) == "" {
		return ErrSubjectRequired
	}
	if !m.Role.Valid() {
		return &InvalidRoleError{Role: string(m.Role)}
	}
	if !m.Status.Valid() {
		return fmt.Errorf("tenant: member unsupported status %q", m.Status)
	}
	return nil
}

// Active 判断成员是否在册。
func (m Member) Active() bool {
	return m.Status == MemberStatusActive
}

// WithRole 返回改角色后的副本；角色不变时原样返回。
func (m Member) WithRole(role MemberRole, now time.Time) (Member, error) {
	if !role.Valid() {
		return m, &InvalidRoleError{Role: string(role)}
	}
	if m.Role == role {
		return m, nil
	}
	next := m
	next.Role = role
	next.UpdatedAt = now.UTC()
	return next, nil
}

// ValidateMemberStatus 校验成员状态取值，供入参校验使用。
//
// 与 Member.Validate 的区别：本函数只服务「调用方传入的值」，
// 返回可被翻译成 400 的 InvalidStatusError；Validate 用于仓储回读的脏数据防御，
// 保持裸错误以免把数据损坏误报成参数问题。
func ValidateMemberStatus(status MemberStatus) error {
	if !status.Valid() {
		return &InvalidStatusError{Status: string(status)}
	}
	return nil
}

// WithStatus 返回改状态后的副本；状态不变时原样返回。
func (m Member) WithStatus(status MemberStatus, now time.Time) (Member, error) {
	if err := ValidateMemberStatus(status); err != nil {
		return m, err
	}
	if m.Status == status {
		return m, nil
	}
	next := m
	next.Status = status
	next.UpdatedAt = now.UTC()
	return next, nil
}
