package domain

import (
	"strings"
	"time"
)

// 账号字段上限，与 migrations/000011 的列宽一致。
const (
	// MaxSubjectLen 登录主体最大字节数（规范化后）。
	MaxSubjectLen = 255
	// MaxMerchantIDLen B 端商户 ID 最大字节数。
	MaxMerchantIDLen = 64
	// MaxAccountIDLen 雪花 ID 字符串最大长度，防止异常入参写坏 BIGINT 列。
	MaxAccountIDLen = 64
)

// Channel 身份域端别。与 IAM 的 admin / biz 不同，身份域额外覆盖 C 端。
type Channel string

const (
	// ChannelAdmin 运营后台（A 端）。
	ChannelAdmin Channel = "admin"
	// ChannelBiz 商家端（B 端）。
	ChannelBiz Channel = "biz"
	// ChannelC 消费者端（C 端）。
	ChannelC Channel = "c"
)

// Validate 校验端别取值。
func (c Channel) Validate() error {
	switch c {
	case ChannelAdmin, ChannelBiz, ChannelC:
		return nil
	default:
		return errInvalid(ErrInvalidChannel, "channel %q must be admin, biz or c", string(c))
	}
}

// String 返回端别字面量。
func (c Channel) String() string { return string(c) }

// NormalizeSubject 规范化登录主体：去首尾空白并转小写。
//
// 唯一键为 (channel, subject)，而数据库排序规则 utf8mb4_0900_ai_ci 大小写不敏感；
// 若领域层不做统一，`Alice` 与 `alice` 会被数据库判为重复但应用侧以为不同，
// 造成「注册成功却无法登录」的隐蔽故障。
func NormalizeSubject(subject string) string {
	return strings.ToLower(strings.TrimSpace(subject))
}

// AccountStatus 账号生命周期状态。
type AccountStatus string

const (
	// AccountStatusPending 已创建但尚未完成首次登录。
	AccountStatusPending AccountStatus = "pending"
	// AccountStatusActive 可正常登录。
	AccountStatusActive AccountStatus = "active"
	// AccountStatusLocked 失败次数达到阈值，锁定期内拒绝登录。
	AccountStatusLocked AccountStatus = "locked"
	// AccountStatusDisabled 管理员停用或已清理，任何登录路径均拒绝。
	AccountStatusDisabled AccountStatus = "disabled"
)

// Validate 校验账号状态取值。
func (s AccountStatus) Validate() error {
	switch s {
	case AccountStatusPending, AccountStatusActive, AccountStatusLocked, AccountStatusDisabled:
		return nil
	default:
		return errInvalid(ErrInvalidAccountStatus, "account status %q is not supported", string(s))
	}
}

// String 返回状态字面量。
func (s AccountStatus) String() string { return string(s) }

// Account 是身份域的账号聚合根。
//
// 不变量：
//   - (channel, subject) 全局唯一，subject 不可复用（软删后仍占用唯一键）；
//   - TokenVersion ≥ 1 且只增不减，表达账号级撤销；
//   - B 端账号必须携带 merchant_id，A / C 端必须为空；
//   - 停用、软删、改密均递增 TokenVersion 并撤销会话（会话撤销由用例编排）。
type Account struct {
	// ID 雪花 ID。
	ID string
	// Channel 账号所属端别。
	Channel Channel
	// Subject 规范化后的登录主体；同一端别内唯一。
	Subject string
	// Status 生命周期状态。
	Status AccountStatus
	// TokenVersion 账号级撤销版本，签发令牌写入 ver，校验时比较。
	TokenVersion int64
	// Version 乐观锁版本，每次变更递增；与 TokenVersion 语义独立。
	Version int64
	// MerchantID B 端商户 ID；其他端别为空串。
	MerchantID string
	// PasswordUpdatedAt 最近一次改密时间；nil 表示从未设置密码。
	PasswordUpdatedAt *time.Time
	// LockedUntil 锁定到期时间（UTC）；仅 status=locked 时有值。
	LockedUntil *time.Time
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt 软删时间；nil 表示未删除。
	DeletedAt *time.Time
}

// NewAccountParams 创建账号的入参。
type NewAccountParams struct {
	ID         string
	Channel    Channel
	Subject    string
	MerchantID string
	// Now 创建时间；零值时取当前 UTC 时间。
	Now time.Time
}

// NewAccount 创建处于 pending 状态、TokenVersion 与 Version 均为 1 的账号。
func NewAccount(p NewAccountParams) (*Account, error) {
	if strings.TrimSpace(p.ID) == "" || len(p.ID) > MaxAccountIDLen {
		return nil, errInvalid(ErrEmptyInput, "account id is required and must be at most %d bytes", MaxAccountIDLen)
	}
	if err := p.Channel.Validate(); err != nil {
		return nil, err
	}
	subject := NormalizeSubject(p.Subject)
	if subject == "" {
		return nil, errInvalid(ErrEmptyInput, "account subject is required")
	}
	if len(subject) > MaxSubjectLen {
		return nil, errInvalid(ErrInvalidAccount, "account subject exceeds %d bytes", MaxSubjectLen)
	}
	if err := validateMerchant(p.Channel, p.MerchantID); err != nil {
		return nil, err
	}
	now := utcNow(p.Now)
	return &Account{
		ID:           p.ID,
		Channel:      p.Channel,
		Subject:      subject,
		Status:       AccountStatusPending,
		TokenVersion: 1,
		Version:      1,
		MerchantID:   p.MerchantID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// validateMerchant 校验端别与商户的一致性。
func validateMerchant(channel Channel, merchantID string) error {
	if len(merchantID) > MaxMerchantIDLen {
		return errInvalid(ErrInvalidAccount, "merchant id exceeds %d bytes", MaxMerchantIDLen)
	}
	if channel == ChannelBiz {
		if strings.TrimSpace(merchantID) == "" {
			return errInvalid(ErrEmptyInput, "biz account requires merchant id")
		}
		return nil
	}
	if merchantID != "" {
		return errInvalid(ErrInvalidAccount, "channel %q must not carry merchant id", string(channel))
	}
	return nil
}

// IsDeleted 报告账号是否已软删。
func (a *Account) IsDeleted() bool { return a.DeletedAt != nil }

// Activate 完成首次登录激活；已激活时幂等返回。
func (a *Account) Activate(now time.Time) error {
	if err := a.ensureNotDeleted(); err != nil {
		return err
	}
	switch a.Status {
	case AccountStatusPending:
		a.Status = AccountStatusActive
		a.LockedUntil = nil
		a.touch(now)
		return nil
	case AccountStatusActive:
		return nil
	default:
		return errConflict(ErrInvalidAccountTransition,
			"account %s cannot transition from %s to active", a.ID, a.Status)
	}
}

// Lock 把 active 账号置为 locked，until 必须晚于 now。
func (a *Account) Lock(until, now time.Time) error {
	if err := a.ensureNotDeleted(); err != nil {
		return err
	}
	if a.Status != AccountStatusActive {
		return errConflict(ErrInvalidAccountTransition,
			"account %s must be active to lock, current status %s", a.ID, a.Status)
	}
	if until.IsZero() {
		return errInvalid(ErrInvalidAccountTransition, "lock expiry is required")
	}
	expiry := until.UTC()
	if !expiry.After(utcNow(now)) {
		return errInvalid(ErrInvalidAccountTransition, "lock expiry must be after now")
	}
	a.Status = AccountStatusLocked
	a.LockedUntil = &expiry
	a.touch(now)
	return nil
}

// Unlock 由管理员解锁；lock 未到期也允许强制解除。
func (a *Account) Unlock(now time.Time) error {
	if err := a.ensureNotDeleted(); err != nil {
		return err
	}
	if a.Status != AccountStatusLocked {
		return errConflict(ErrInvalidAccountTransition,
			"account %s is %s, not locked", a.ID, a.Status)
	}
	a.Status = AccountStatusActive
	a.LockedUntil = nil
	a.touch(now)
	return nil
}

// TryUnlockExpired 在锁定期到期后自动恢复 active，返回是否发生迁移。
//
// 登录成功时由用例调用：锁定期已过但状态仍为 locked 的账号可以登录，
// 成功后自动回到 active，避免管理员介入。
func (a *Account) TryUnlockExpired(now time.Time) (bool, error) {
	if err := a.ensureNotDeleted(); err != nil {
		return false, err
	}
	if a.Status != AccountStatusLocked || a.LockedUntil == nil {
		return false, nil
	}
	if a.LockedUntil.After(now) {
		return false, nil
	}
	a.Status = AccountStatusActive
	a.LockedUntil = nil
	a.touch(now)
	return true, nil
}

// Disable 停用账号：pending / active / locked 均可停用，并递增 TokenVersion。
//
// 不在此处撤销会话：跨聚合写入由用例在同一事务内编排，领域层只表达账号事实。
func (a *Account) Disable(now time.Time) error {
	if err := a.ensureNotDeleted(); err != nil {
		return err
	}
	if a.Status == AccountStatusDisabled {
		return nil
	}
	a.Status = AccountStatusDisabled
	a.LockedUntil = nil
	a.TokenVersion++
	a.touch(now)
	return nil
}

// Enable 由管理员恢复 disabled 账号；不恢复旧会话，用户必须重新登录。
func (a *Account) Enable(now time.Time) error {
	if err := a.ensureNotDeleted(); err != nil {
		return err
	}
	switch a.Status {
	case AccountStatusDisabled:
		a.Status = AccountStatusActive
		a.touch(now)
		return nil
	case AccountStatusActive:
		return nil
	default:
		return errConflict(ErrInvalidAccountTransition,
			"account %s cannot transition from %s to active", a.ID, a.Status)
	}
}

// Delete 软删账号：置为 disabled 并递增 TokenVersion，subject 永久占用唯一键。
func (a *Account) Delete(now time.Time) error {
	if a.IsDeleted() {
		return errConflict(ErrAccountDeleted, "account %s is already deleted", a.ID)
	}
	a.Status = AccountStatusDisabled
	a.LockedUntil = nil
	a.TokenVersion++
	deletedAt := utcNow(now)
	a.DeletedAt = &deletedAt
	a.touch(now)
	return nil
}

// ChangePassword 记录改密并递增 TokenVersion；调用方须同时撤销全部会话。
func (a *Account) ChangePassword(now time.Time) error {
	if err := a.ensureNotDeleted(); err != nil {
		return err
	}
	if a.Status != AccountStatusActive {
		return errConflict(ErrInvalidAccountTransition,
			"account %s must be active to change password, current status %s", a.ID, a.Status)
	}
	changedAt := utcNow(now)
	a.PasswordUpdatedAt = &changedAt
	a.TokenVersion++
	a.touch(now)
	return nil
}

// CanAuthenticate 判断账号当前是否允许发起登录。
//
// pending 允许登录（首次登录成功后激活）；locked 在锁定期到期后允许重试，
// 成功登录由用例调用 TryUnlockExpired 恢复 active。
func (a *Account) CanAuthenticate(now time.Time) bool {
	if a.IsDeleted() {
		return false
	}
	switch a.Status {
	case AccountStatusDisabled:
		return false
	case AccountStatusLocked:
		return a.LockedUntil == nil || !a.LockedUntil.After(now)
	default:
		return true
	}
}

// ensureNotDeleted 统一拒绝已软删账号上的写操作。
func (a *Account) ensureNotDeleted() error {
	if a.IsDeleted() {
		return errConflict(ErrAccountDeleted, "account %s is deleted", a.ID)
	}
	return nil
}

// touch 更新审计字段并递增乐观锁版本。
func (a *Account) touch(now time.Time) {
	a.UpdatedAt = utcNow(now)
	a.Version++
}

// utcNow 统一把时间归一为 UTC；零值回退到当前时间。
func utcNow(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}
