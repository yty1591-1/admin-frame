package domain

import (
	"strings"
	"time"
)

// SceneClass 是业务场景的分类维度：偏好与静默时段按类生效，
// 不要求用户逐场景声明。
type SceneClass string

// 场景分类取值。
const (
	// ClassTransactional 事务类（验证码、邀请、安全告警）：不可退订。
	ClassTransactional SceneClass = "transactional"
	// ClassOperational 运营类（账单、状态变更、租户停用提醒）：可退订。
	ClassOperational SceneClass = "operational"
	// ClassMarketing 营销类（活动、产品通知）：可退订，受静默时段约束。
	ClassMarketing SceneClass = "marketing"
)

// Valid 判断分类是否已定义。
func (c SceneClass) Valid() bool {
	switch c {
	case ClassTransactional, ClassOperational, ClassMarketing:
		return true
	default:
		return false
	}
}

// OptOutAllowed 判断该分类是否允许用户退订：
// 事务类承载账号安全与合同动作，退订等于放弃必要告知，领域层直接禁止。
func (c SceneClass) OptOutAllowed() bool {
	return c != ClassTransactional
}

// SubjectType 是偏好主体类型。
type SubjectType string

// 主体类型取值。
const (
	// SubjectUser 租户内的用户主体。
	SubjectUser SubjectType = "user"
	// SubjectTenant 租户整体（租户级开关，覆盖用户缺省值）。
	SubjectTenant SubjectType = "tenant"
)

// Valid 判断主体类型是否已定义。
func (s SubjectType) Valid() bool {
	switch s {
	case SubjectUser, SubjectTenant:
		return true
	default:
		return false
	}
}

// 长度上限（与迁移 000016 列宽对齐）。
const (
	// MaxSubjectIDLength 偏好主体 ID 上限。
	MaxSubjectIDLength = 64
	// MaxBlacklistReasonLength 黑名单原因上限。
	MaxBlacklistReasonLength = 255
)

// BlacklistSource 是黑名单条目的来源。
type BlacklistSource string

// 来源取值。
const (
	// BlacklistComplaint 收件人投诉（供应商回执）。
	BlacklistComplaint BlacklistSource = "complaint"
	// BlacklistBounce 硬退信（地址确认失效）。
	BlacklistBounce BlacklistSource = "bounce"
	// BlacklistManual 运营手工加入。
	BlacklistManual BlacklistSource = "manual"
)

// Valid 判断来源是否已定义。
func (s BlacklistSource) Valid() bool {
	switch s {
	case BlacklistComplaint, BlacklistBounce, BlacklistManual:
		return true
	default:
		return false
	}
}

// Preference 是一条渠道偏好：主体在某场景分类下对某渠道的开关。
//
// 命中语义：记录存在且 Enabled=false → 该分类该渠道不发（rejected）；
// 记录不存在 → 默认允许（opt-in 模型，退订是显式动作）。
type Preference struct {
	// TenantID 租户隔离键；平台级偏好用 PlatformTenantID。
	TenantID string
	// SubjectType 主体类型。
	SubjectType SubjectType
	// SubjectID 主体 ID。
	SubjectID string
	// SceneClass 场景分类。
	SceneClass SceneClass
	// Channel 渠道。
	Channel Channel
	// Enabled false = 已退订。
	Enabled bool
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// Validate 校验回读自洽性。
func (p Preference) Validate() error {
	if strings.TrimSpace(p.TenantID) == "" {
		return ErrTenantIDRequired
	}
	if !p.SubjectType.Valid() {
		return &InvalidSubjectTypeError{Value: string(p.SubjectType)}
	}
	if strings.TrimSpace(p.SubjectID) == "" || len([]rune(p.SubjectID)) > MaxSubjectIDLength {
		return ErrSubjectIDRequired
	}
	if !p.SceneClass.Valid() {
		return &InvalidSceneClassError{Value: string(p.SceneClass)}
	}
	if !p.Channel.Valid() {
		return &InvalidChannelError{Channel: string(p.Channel)}
	}
	return nil
}

// Blacklist 是一条渠道黑名单：目标在渠道维度的永久拒绝。
//
// TargetHMAC 是收件人地址的加盐 HMAC（十六进制）：库中不留明文地址，
// 查询时对目标做同样 HMAC 后比对——拖库无法还原投递目标。
type Blacklist struct {
	// TenantID 租户隔离键；平台级黑名单用 PlatformTenantID（全租户生效）。
	TenantID string
	// Channel 渠道。
	Channel Channel
	// TargetHMAC 目标地址的加盐 HMAC 摘要（64 位十六进制）。
	TargetHMAC string
	// Source 进入黑名单的来源。
	Source BlacklistSource
	// Reason 运营可读的原因。
	Reason string
	// CreatedBy 操作主体（系统来源取系统标识）。
	CreatedBy string
	// CreatedAt 加入时间。
	CreatedAt time.Time
}

// Validate 校验回读自洽性。
func (b Blacklist) Validate() error {
	if strings.TrimSpace(b.TenantID) == "" {
		return ErrTenantIDRequired
	}
	if !b.Channel.Valid() {
		return &InvalidChannelError{Channel: string(b.Channel)}
	}
	if len(b.TargetHMAC) != 64 {
		return ErrTargetHMACInvalid
	}
	if !b.Source.Valid() {
		return &InvalidBlacklistSourceError{Value: string(b.Source)}
	}
	return nil
}

// NewPreference 构造偏好记录；退订事务类场景在领域层直接拒绝。
func NewPreference(tenantID string, subjectType SubjectType, subjectID string,
	class SceneClass, channel Channel, enabled bool, now time.Time) (Preference, error) {
	p := Preference{
		TenantID:    strings.TrimSpace(tenantID),
		SubjectType: subjectType,
		SubjectID:   strings.TrimSpace(subjectID),
		SceneClass:  class,
		Channel:     channel,
		Enabled:     enabled,
		CreatedAt:   now.UTC(),
		UpdatedAt:   now.UTC(),
	}
	if !enabled && !class.OptOutAllowed() {
		return Preference{}, ErrTransactionalOptOut
	}
	if err := p.Validate(); err != nil {
		return Preference{}, err
	}
	return p, nil
}

// NewBlacklist 构造黑名单条目；目标 HMAC 由应用层计算后传入。
func NewBlacklist(tenantID string, channel Channel, targetHMAC string,
	source BlacklistSource, reason, createdBy string, now time.Time) (Blacklist, error) {
	b := Blacklist{
		TenantID:   strings.TrimSpace(tenantID),
		Channel:    channel,
		TargetHMAC: strings.ToLower(strings.TrimSpace(targetHMAC)),
		Source:     source,
		Reason:     strings.TrimSpace(reason),
		CreatedBy:  strings.TrimSpace(createdBy),
		CreatedAt:  now.UTC(),
	}
	if len(b.Reason) > MaxBlacklistReasonLength {
		return Blacklist{}, ErrBlacklistReasonTooLong
	}
	if err := b.Validate(); err != nil {
		return Blacklist{}, err
	}
	return b, nil
}
