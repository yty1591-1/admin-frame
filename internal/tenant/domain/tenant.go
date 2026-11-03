// Package domain 定义租户域实体、状态机与仓储契约（ENT-09）。
//
// 本包不依赖仓储实现、HTTP 或 IAM：租户是隔离边界（ADR-0006），
// 状态流转规则必须能脱离基础设施单测，否则规则会散落在 handler 与 SQL 里。
package domain

import (
	"fmt"
	"strings"
	"time"
)

// Status 是租户生命周期状态。
type Status string

// 租户状态取值；顺序即生命周期顺序，terminated 为终态。
const (
	// StatusPending 已创建、尚未开通：不接受业务流量。
	StatusPending Status = "pending"
	// StatusActive 正常服务。
	StatusActive Status = "active"
	// StatusSuspended 停用：拒绝访问，数据保留，可恢复。
	StatusSuspended Status = "suspended"
	// StatusTerminated 已终止：终态，不可恢复；数据按策略处置。
	StatusTerminated Status = "terminated"
)

// MaxNameLength 租户名称长度上限（与 tenant.name VARCHAR(128) 对齐）。
const MaxNameLength = 128

// MaxReasonLength 状态原因长度上限（与 tenant.status_reason VARCHAR(255) 对齐）。
const MaxReasonLength = 255

// 元数据字段长度上限（T-11），与迁移 000013 的列宽一一对齐。
const (
	// MaxCodeLength 业务编码长度上限（tenant.code VARCHAR(64)）。
	MaxCodeLength = 64
	// MaxContactNameLength 联系人姓名上限（tenant.contact_name VARCHAR(64)）。
	MaxContactNameLength = 64
	// MaxContactEmailLength 联系邮箱上限（tenant.contact_email VARCHAR(128)）。
	MaxContactEmailLength = 128
	// MaxIndustryLength 行业上限（tenant.industry VARCHAR(64)）。
	MaxIndustryLength = 64
	// MaxTimezoneLength 时区上限（tenant.timezone VARCHAR(64)）。
	MaxTimezoneLength = 64
	// MaxLocaleLength 语言标签上限（tenant.locale VARCHAR(16)）。
	MaxLocaleLength = 16
	// MaxExternalIDLength 外部系统 ID 上限（tenant.external_id VARCHAR(128)）。
	MaxExternalIDLength = 128
)

// Valid 判断状态是否为已定义取值。
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusActive, StatusSuspended, StatusTerminated:
		return true
	default:
		return false
	}
}

// Terminal 判断是否为终态：终态租户不允许任何后续迁移。
func (s Status) Terminal() bool {
	return s == StatusTerminated
}

// Serving 判断该状态是否允许业务访问；停用与待开通都不放行。
func (s Status) Serving() bool {
	return s == StatusActive
}

// transitions 是状态机的唯一真相：只列合法迁移，未列出的一律拒绝。
//
// 刻意不允许 suspended → pending（回退到未开通会让已开通的租户丢失开通事实），
// 也不允许 terminated → 任意状态（终止需走新租户，避免「复活」绕过合同与结算）。
var transitions = map[Status][]Status{
	StatusPending:   {StatusActive, StatusTerminated},
	StatusActive:    {StatusSuspended, StatusTerminated},
	StatusSuspended: {StatusActive, StatusTerminated},
}

// CanTransitionTo 判断从当前状态迁移到目标状态是否合法。
func (s Status) CanTransitionTo(target Status) bool {
	for _, allowed := range transitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

// Transitions 返回迁移矩阵的快照（副本），供测试快照与文档生成消费。
//
// 返回副本而非内部 map：调用方改动结果不得影响状态机本身。
// 矩阵变更必须同步更新 TestTransitionMatrixSnapshot 的快照并通过评审。
func Transitions() map[Status][]Status {
	out := make(map[Status][]Status, len(transitions))
	for from, targets := range transitions {
		copied := make([]Status, len(targets))
		copy(copied, targets)
		out[from] = copied
	}
	return out
}

// Tenant 是租户实体。
type Tenant struct {
	// ID 租户标识，同时是 B 端各表 merchant_id 的取值（ADR-0006）。
	ID string
	// Code 业务编码，全局唯一（T-11）；创建后不可变——它是外部系统
	// （CRM / 结算）的对账引用，改了等于改别人的主键。
	Code string
	// Name 租户名称。
	Name string
	// Status 当前状态。
	Status Status
	// PlanID 开账时使用的套餐（T-24）；空表示历史数据未回填。
	// 套餐是模板：配额已在创建时物化进 tenant_quota，本字段仅留痕与报表用。
	PlanID string
	// StatusReason 最近一次状态变更原因（审计之外的运营说明）。
	StatusReason string
	// ContactName 联系人姓名（T-11）；运营补录，可空。
	ContactName string
	// ContactEmail 联系邮箱（T-11）；可空。
	ContactEmail string
	// Industry 所属行业（T-11）；可空。
	Industry string
	// Timezone IANA 时区（T-11）；可空。
	Timezone string
	// Locale 语言标签（T-11）；可空。
	Locale string
	// ExternalID 外部系统 ID（T-11）；可空，格式由对方系统约定。
	ExternalID string
	// Version 乐观锁版本；每次成功迁移或资料变更 +1。
	Version int64
	// SuspendedAt 最近一次停用时间；恢复后保留，供回溯。
	SuspendedAt *time.Time
	// TerminatedAt 终止时间；非空即终态。
	TerminatedAt *time.Time
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// UpdatedAt 最近更新时间。
	UpdatedAt time.Time
}

// validCode 只放行小写字母、数字与 `.` `_` `-`。
//
// 编码会出现在 URL、对账文件与配置引用里；限定字符集避免它变成
// 注入点或在不同系统间被转义改写（与套餐 code、配额资源键同一口径）。
func validCode(code string) bool {
	if code == "" {
		return false
	}
	for _, r := range code {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// validateCode 校验业务编码并返回规范化值。
func validateCode(code string) (string, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return "", ErrCodeRequired
	}
	if len([]rune(trimmed)) > MaxCodeLength {
		return "", ErrCodeTooLong
	}
	if !validCode(trimmed) {
		return "", &InvalidCodeError{Code: trimmed}
	}
	return trimmed, nil
}

// NormalizeCode 校验并规范化业务编码（T-11）：去首尾空白，空值 / 超长 /
// 含非法字符分别返回对应领域错误。创建入口未提供编码时由应用层用
// 租户 ID 兜底后再走本校验。
func NormalizeCode(code string) (string, error) {
	return validateCode(code)
}

// NewTenant 构造待开通（pending）租户；名称去空白并校验长度。
//
// 业务编码（T-11）默认取租户 ID 兜底——与迁移 000013 的存量回填策略
// 同源；应用层在创建方显式提供编码时覆盖该默认值。
func NewTenant(id, name string, now time.Time) (Tenant, error) {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return Tenant{}, fmt.Errorf("tenant: id is required")
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return Tenant{}, ErrNameRequired
	}
	if len([]rune(trimmedName)) > MaxNameLength {
		return Tenant{}, ErrNameTooLong
	}
	return Tenant{
		ID:        trimmedID,
		Code:      trimmedID,
		Name:      trimmedName,
		Status:    StatusPending,
		Version:   1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

// Validate 校验仓储回读的实体自洽性。
//
// 用于在应用层防御「库里有脏数据」：状态枚举非法或终态缺终止时间时，
// 宁可报错也不要把不一致的状态当作正常数据继续流转。
func (t Tenant) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("tenant: id is required")
	}
	if strings.TrimSpace(t.Name) == "" {
		return ErrNameRequired
	}
	if _, err := validateCode(t.Code); err != nil {
		return err
	}
	if !t.Status.Valid() {
		return fmt.Errorf("tenant: unsupported status %q", t.Status)
	}
	if t.Status == StatusTerminated && t.TerminatedAt == nil {
		return fmt.Errorf("tenant: terminated tenant %s has no terminated_at", t.ID)
	}
	if t.Version <= 0 {
		return fmt.Errorf("tenant: version must be positive, got %d", t.Version)
	}
	return validateMetadata(t.ContactName, t.ContactEmail, t.Industry, t.Timezone, t.Locale, t.ExternalID)
}

// Profile 是租户资料的可变子集（T-11）：名称 + 元数据。
//
// 编码与状态不在其中：编码不可变，状态走状态机，两者都不允许
// 从「改资料」的入口被顺手改掉。
type Profile struct {
	// Name 租户名称。
	Name string
	// ContactName 联系人姓名；空串表示清空。
	ContactName string
	// ContactEmail 联系邮箱；空串表示清空。
	ContactEmail string
	// Industry 所属行业；空串表示清空。
	Industry string
	// Timezone IANA 时区；空串表示清空。
	Timezone string
	// Locale 语言标签；空串表示清空。
	Locale string
	// ExternalID 外部系统 ID；空串表示清空。
	ExternalID string
}

// WithProfile 以目标资料生成新副本并递增版本。
//
// 版本与状态迁移共用一个计数器：资料编辑与停用互相构成 CAS 冲突，
// 并发「改名 + 停用」不会出现后写者覆盖前写者丢字段的情况。
// 各字段先规范化（去首尾空白），空串语义是「清空该字段」。
func (t Tenant) WithProfile(p Profile, now time.Time) (Tenant, error) {
	trimmedName := strings.TrimSpace(p.Name)
	if trimmedName == "" {
		return t, ErrNameRequired
	}
	if len([]rune(trimmedName)) > MaxNameLength {
		return t, ErrNameTooLong
	}
	metadata := metadataStrings{
		contactName:  strings.TrimSpace(p.ContactName),
		contactEmail: strings.TrimSpace(p.ContactEmail),
		industry:     strings.TrimSpace(p.Industry),
		timezone:     strings.TrimSpace(p.Timezone),
		locale:       strings.TrimSpace(p.Locale),
		externalID:   strings.TrimSpace(p.ExternalID),
	}
	if err := metadata.validate(); err != nil {
		return t, err
	}

	next := t
	next.Name = trimmedName
	next.ContactName = metadata.contactName
	next.ContactEmail = metadata.contactEmail
	next.Industry = metadata.industry
	next.Timezone = metadata.timezone
	next.Locale = metadata.locale
	next.ExternalID = metadata.externalID
	next.Version = t.Version + 1
	next.UpdatedAt = now.UTC()
	return next, nil
}

// metadataStrings 收集元数据各字段，供统一长度 / 格式校验。
type metadataStrings struct {
	contactName  string
	contactEmail string
	industry     string
	timezone     string
	locale       string
	externalID   string
}

// validate 校验元数据：长度上限 + 邮箱与时区的基本格式。
//
// 校验刻意从宽：行业 / 语言是运营自由文本（只拦超长），时区与语言只限
// 字符集（字母数字与 `_` `-` `.` `/`）——完整 IANA 目录校验需要运行环境
// 携带 tzdata，放进领域层会让单测依赖操作系统时区库；真正能否解析在
// 使用方（账期计算）失败时自然会暴露。邮箱只查「含 @、无空白」——
// 完整 RFC 校验交给投递前的验证邮件（ENT-10）。
func (m metadataStrings) validate() error {
	for _, check := range []struct {
		field string
		value string
		max   int
	}{
		{"contact_name", m.contactName, MaxContactNameLength},
		{"contact_email", m.contactEmail, MaxContactEmailLength},
		{"industry", m.industry, MaxIndustryLength},
		{"timezone", m.timezone, MaxTimezoneLength},
		{"locale", m.locale, MaxLocaleLength},
		{"external_id", m.externalID, MaxExternalIDLength},
	} {
		if len([]rune(check.value)) > check.max {
			return &FieldTooLongError{Field: check.field, Max: check.max}
		}
	}
	if m.contactEmail != "" {
		if strings.ContainsAny(m.contactEmail, " \t\r\n") || !strings.Contains(m.contactEmail, "@") {
			return &InvalidEmailError{Email: m.contactEmail}
		}
	}
	for _, check := range []struct {
		field, value string
	}{
		{"timezone", m.timezone},
		{"locale", m.locale},
	} {
		if !validLocaleToken(check.value) {
			return &InvalidFieldError{Field: check.field, Value: check.value}
		}
	}
	return nil
}

// validLocaleToken 放行时区 / 语言标签的安全字符集。
func validLocaleToken(value string) bool {
	if value == "" {
		return true // 空串=清空，合法。
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '/':
		default:
			return false
		}
	}
	return true
}

// validateMetadata 校验回读实体的元数据字段。
func validateMetadata(contactName, contactEmail, industry, timezone, locale, externalID string) error {
	return metadataStrings{
		contactName:  contactName,
		contactEmail: contactEmail,
		industry:     industry,
		timezone:     timezone,
		locale:       locale,
		externalID:   externalID,
	}.validate()
}

// Transition 迁移到目标状态并递增版本号。
//
// 停用与终止必须给出原因：这两个动作会影响客户可用性与合同，不能只有操作人没有理由。
// 返回的 Tenant 是迁移后的副本，调用方需把它交给仓储做 CAS 写入。
func (t Tenant) Transition(target Status, reason string, now time.Time) (Tenant, error) {
	if !target.Valid() {
		return t, fmt.Errorf("tenant: unsupported status %q", target)
	}
	if t.Status.Terminal() {
		return t, ErrTerminated
	}
	if !t.Status.CanTransitionTo(target) {
		return t, &InvalidTransitionError{From: t.Status, To: target}
	}
	trimmedReason := strings.TrimSpace(reason)
	if (target == StatusSuspended || target == StatusTerminated) && trimmedReason == "" {
		return t, ErrReasonRequired
	}
	if len([]rune(trimmedReason)) > MaxReasonLength {
		return t, ErrReasonTooLong
	}

	next := t
	next.Status = target
	next.StatusReason = trimmedReason
	next.Version = t.Version + 1
	next.UpdatedAt = now.UTC()
	switch target {
	case StatusSuspended:
		moment := now.UTC()
		next.SuspendedAt = &moment
	case StatusTerminated:
		moment := now.UTC()
		next.TerminatedAt = &moment
	}
	return next, nil
}
