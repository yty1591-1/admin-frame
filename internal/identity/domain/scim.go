package domain

// 本文件定义 SCIM 2.0 服务端资源（docs/identity-authn.md §21.1）。
//
// 边界：
//   - 只做服务端：IdP 推送 User / Group，本平台不反向拉取；
//   - User 不新建用户表，复用 identity_account + identity_oauth_identity；
//   - Group 独立落 identity_scim_group / identity_scim_group_member；
//   - 令牌只存 SHA-256，每 provider 同时最多 2 个生效令牌。

import (
	"strings"
	"time"
)

// SCIM 资源字段上限，与 migrations/000027 的列宽一致。
const (
	// MaxScimProviderLen provider 标识最大字节数（列宽 64，含 scim: 前缀）。
	MaxScimProviderLen = 64
	// MaxScimExternalIDLen 外部 ID 最大字节数；与 provider_subject 列宽一致。
	MaxScimExternalIDLen = 255
	// MaxScimGroupNameLen 组展示名最大字节数。
	MaxScimGroupNameLen = 255
	// MaxScimTokenIDLen 令牌主键字符串最大长度。
	MaxScimTokenIDLen = 64
	// MaxScimGroupIDLen 组主键字符串最大长度。
	MaxScimGroupIDLen = 64
)

// ScimTokenStatus 是 SCIM 令牌生命周期状态。
//
//   - active：可用；每 provider 最多 2 个；
//   - retiring：已退役但仍在宽限期内可用，到期由清理任务转为 revoked；
//   - revoked：立即不可用，仅保留指纹供审计。
type ScimTokenStatus string

const (
	// ScimTokenActive 生效中。
	ScimTokenActive ScimTokenStatus = "active"
	// ScimTokenRetiring 退役宽限期。
	ScimTokenRetiring ScimTokenStatus = "retiring"
	// ScimTokenRevoked 已吊销。
	ScimTokenRevoked ScimTokenStatus = "revoked"
)

// Validate 校验状态取值。
func (s ScimTokenStatus) Validate() error {
	switch s {
	case ScimTokenActive, ScimTokenRetiring, ScimTokenRevoked:
		return nil
	default:
		return errInvalid(ErrInvalidSCIMToken, "scim token status %q is not supported", string(s))
	}
}

// ScimToken 是 SCIM 目录同步令牌。
//
// 不变量：
//   - TokenHash 全局唯一，只存 SHA-256；
//   - 每 provider 同时最多 2 个 active；
//   - revoked 后不可恢复，只能重新签发。
type ScimToken struct {
	// ID 雪花 ID。
	ID string
	// Provider 目录提供方标识（不含 scim: 前缀，如 okta）。
	Provider string
	// TokenHash 令牌的 SHA-256 十六进制摘要；禁止存原文。
	TokenHash string
	// Fingerprint 展示用指纹（sha256:xxxxxxxxxxxx）。
	Fingerprint string
	// Status 生命周期状态。
	Status ScimTokenStatus
	// ExpiresAt 退役宽限到期时间；仅 retiring 有值。
	ExpiresAt *time.Time
	// CreatedBy 签发人主体 ID。
	CreatedBy string
	// CreatedAt / RevokedAt UTC。
	CreatedAt time.Time
	RevokedAt *time.Time
}

// NewScimTokenParams 创建令牌的入参。
type NewScimTokenParams struct {
	ID          string
	Provider    string
	TokenHash   string
	Fingerprint string
	CreatedBy   string
	Now         time.Time
}

// NewScimToken 创建处于 active 状态的令牌。
func NewScimToken(p NewScimTokenParams) (*ScimToken, error) {
	token := &ScimToken{
		ID:          strings.TrimSpace(p.ID),
		Provider:    strings.TrimSpace(p.Provider),
		TokenHash:   strings.TrimSpace(p.TokenHash),
		Fingerprint: strings.TrimSpace(p.Fingerprint),
		Status:      ScimTokenActive,
		CreatedBy:   strings.TrimSpace(p.CreatedBy),
		CreatedAt:   utcNow(p.Now),
	}
	if err := token.Validate(); err != nil {
		return nil, err
	}
	return token, nil
}

// Validate 校验令牌字段。
func (t *ScimToken) Validate() error {
	if t.ID == "" || len(t.ID) > MaxScimTokenIDLen {
		return errInvalid(ErrEmptyInput, "scim token id is required and must be at most %d bytes", MaxScimTokenIDLen)
	}
	if t.Provider == "" || len(t.Provider) > MaxScimProviderLen {
		return errInvalid(ErrInvalidSCIMToken, "scim token provider is required and must be at most %d bytes", MaxScimProviderLen)
	}
	if t.TokenHash == "" {
		return errInvalid(ErrInvalidSCIMToken, "scim token hash is required")
	}
	if err := t.Status.Validate(); err != nil {
		return err
	}
	if t.ExpiresAt != nil && t.Status != ScimTokenRetiring {
		return errInvalid(ErrInvalidSCIMToken, "scim token expiry is only allowed for retiring tokens")
	}
	return nil
}

// Usable 报告令牌在 now 时是否可用于认证。
func (t *ScimToken) Usable(now time.Time) bool {
	switch t.Status {
	case ScimTokenActive:
		return true
	case ScimTokenRetiring:
		return t.ExpiresAt == nil || now.Before(*t.ExpiresAt)
	default:
		return false
	}
}

// Retire 把令牌转入退役宽限；grace ≤ 0 表示立即到期。
func (t *ScimToken) Retire(now time.Time, grace time.Duration) {
	if t.Status != ScimTokenActive {
		return
	}
	t.Status = ScimTokenRetiring
	expiry := utcNow(now).Add(grace)
	t.ExpiresAt = &expiry
}

// Revoke 立即吊销令牌；保留指纹与签发人供审计。
func (t *ScimToken) Revoke(now time.Time) {
	if t.Status == ScimTokenRevoked {
		return
	}
	revokedAt := utcNow(now)
	t.Status = ScimTokenRevoked
	t.RevokedAt = &revokedAt
}

// ScimGroup 是 SCIM 组；成员关系单独存放，避免组行随成员变化频繁更新。
type ScimGroup struct {
	// ID 雪花 ID。
	ID string
	// Provider 目录提供方标识。
	Provider string
	// ExternalID 外部 ID，幂等键；同 provider 内唯一。
	ExternalID string
	// DisplayName 组展示名；角色映射键，同 provider 内唯一。
	DisplayName string
	// MappedRole 由 group_role_map 解析出的 A 端角色 code；空串表示未映射。
	MappedRole string
	// Version 乐观锁与 meta.version / ETag 来源。
	Version int64
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt 软删时间；nil 表示未删除。
	DeletedAt *time.Time
}

// NewScimGroupParams 创建组的入参。
type NewScimGroupParams struct {
	ID          string
	Provider    string
	ExternalID  string
	DisplayName string
	MappedRole  string
	Now         time.Time
}

// NewScimGroup 创建组。
func NewScimGroup(p NewScimGroupParams) (*ScimGroup, error) {
	now := utcNow(p.Now)
	group := &ScimGroup{
		ID:          strings.TrimSpace(p.ID),
		Provider:    strings.TrimSpace(p.Provider),
		ExternalID:  strings.TrimSpace(p.ExternalID),
		DisplayName: strings.TrimSpace(p.DisplayName),
		MappedRole:  strings.TrimSpace(p.MappedRole),
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := group.Validate(); err != nil {
		return nil, err
	}
	return group, nil
}

// Validate 校验组字段。
func (g *ScimGroup) Validate() error {
	if g.ID == "" || len(g.ID) > MaxScimGroupIDLen {
		return errInvalid(ErrEmptyInput, "scim group id is required and must be at most %d bytes", MaxScimGroupIDLen)
	}
	if g.Provider == "" || len(g.Provider) > MaxScimProviderLen {
		return errInvalid(ErrInvalidSCIMGroup, "scim group provider is required and must be at most %d bytes", MaxScimProviderLen)
	}
	if g.ExternalID == "" || len(g.ExternalID) > MaxScimExternalIDLen {
		return errInvalid(ErrInvalidSCIMGroup, "scim group externalId is required and must be at most %d bytes", MaxScimExternalIDLen)
	}
	if g.DisplayName == "" || len(g.DisplayName) > MaxScimGroupNameLen {
		return errInvalid(ErrInvalidSCIMGroup, "scim group displayName is required and must be at most %d bytes", MaxScimGroupNameLen)
	}
	if g.Version < 1 {
		return errInvalid(ErrInvalidSCIMGroup, "scim group version must be >= 1")
	}
	return nil
}

// IsDeleted 报告组是否已软删。
func (g *ScimGroup) IsDeleted() bool { return g.DeletedAt != nil }

// Rename 更新展示名与角色映射并递增版本。
func (g *ScimGroup) Rename(displayName, mappedRole string, now time.Time) error {
	if g.IsDeleted() {
		return errConflict(ErrInvalidSCIMGroup, "scim group %s is deleted", g.ID)
	}
	name := strings.TrimSpace(displayName)
	if name == "" || len(name) > MaxScimGroupNameLen {
		return errInvalid(ErrInvalidSCIMGroup, "scim group displayName is required and must be at most %d bytes", MaxScimGroupNameLen)
	}
	g.DisplayName = name
	g.MappedRole = strings.TrimSpace(mappedRole)
	g.touch(now)
	return nil
}

// RebindExternalID 更换外部 ID；同 provider 内仍须唯一。
func (g *ScimGroup) RebindExternalID(externalID string, now time.Time) error {
	if g.IsDeleted() {
		return errConflict(ErrInvalidSCIMGroup, "scim group %s is deleted", g.ID)
	}
	value := strings.TrimSpace(externalID)
	if value == "" || len(value) > MaxScimExternalIDLen {
		return errInvalid(ErrInvalidSCIMGroup, "scim group externalId is required and must be at most %d bytes", MaxScimExternalIDLen)
	}
	g.ExternalID = value
	g.touch(now)
	return nil
}

// Delete 软删组；成员关系由用例在同一事务内清理。
func (g *ScimGroup) Delete(now time.Time) error {
	if g.IsDeleted() {
		return errConflict(ErrInvalidSCIMGroup, "scim group %s is already deleted", g.ID)
	}
	deletedAt := utcNow(now)
	g.DeletedAt = &deletedAt
	g.touch(now)
	return nil
}

// touch 更新审计字段并递增版本。
func (g *ScimGroup) touch(now time.Time) {
	g.UpdatedAt = utcNow(now)
	g.Version++
}

// ScimGroupMember 是组与账号的成员关系。
type ScimGroupMember struct {
	// GroupID 组主键。
	GroupID string
	// AccountID 账号主键；SCIM 的 members[].value 解析结果。
	AccountID string
	// CreatedAt 加入时间（UTC）。
	CreatedAt time.Time
}

// ScimGroupFilter 是组列表查询条件。
type ScimGroupFilter struct {
	// Provider 目录提供方；必填。
	Provider string
	// DisplayName 展示名精确匹配；空表示不过滤。
	DisplayName string
	// ExternalID 外部 ID 精确匹配；空表示不过滤。
	ExternalID string
	// Limit 每页条数；调用方负责上限钳制。
	Limit int
	// Offset 偏移量，按 created_at、id 升序。
	Offset int
}

// ScimTokenListFilter 是令牌列表查询条件。
type ScimTokenListFilter struct {
	// Provider 目录提供方；必填。
	Provider string
}
