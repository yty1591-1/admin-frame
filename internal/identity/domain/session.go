package domain

import (
	"strings"
	"time"
)

// 会话字段上限，与 migrations/000001 的列宽一致。
const (
	// MaxRefreshHashLen 刷新令牌 SHA-256 十六进制摘要长度上限。
	MaxRefreshHashLen = 64
	// MaxRevokeReasonLen 撤销原因最大字节数。
	MaxRevokeReasonLen = 32
)

// SessionStatus 会话状态。
type SessionStatus string

const (
	// SessionStatusActive 当前可用，刷新令牌只能用一次。
	SessionStatusActive SessionStatus = "active"
	// SessionStatusRotated 已被一次成功刷新消耗；再次出现即为重放。
	SessionStatusRotated SessionStatus = "rotated"
	// SessionStatusRevoked 已撤销（登出 / 管理员 / 重放 / 账号停用）。
	SessionStatusRevoked SessionStatus = "revoked"
	// SessionStatusExpired 已过期。
	SessionStatusExpired SessionStatus = "expired"
)

// Validate 校验会话状态取值。
func (s SessionStatus) Validate() error {
	switch s {
	case SessionStatusActive, SessionStatusRotated, SessionStatusRevoked, SessionStatusExpired:
		return nil
	default:
		return errInvalid(ErrInvalidSessionStatus, "session status %q is not supported", string(s))
	}
}

// String 返回状态字面量。
func (s SessionStatus) String() string { return string(s) }

// RevokeReason 撤销原因，写入审计与排障。
type RevokeReason string

const (
	// RevokeReasonLogout 用户主动登出当前会话。
	RevokeReasonLogout RevokeReason = "logout"
	// RevokeReasonLogoutAll 用户登出全部设备。
	RevokeReasonLogoutAll RevokeReason = "logout_all"
	// RevokeReasonReplay 刷新令牌重放，整条会话链被撤销。
	RevokeReasonReplay RevokeReason = "replay"
	// RevokeReasonAdmin 管理员撤销会话。
	RevokeReasonAdmin RevokeReason = "admin"
	// RevokeReasonAccountDisabled 账号被停用。
	RevokeReasonAccountDisabled RevokeReason = "account_disabled"
	// RevokeReasonPasswordChanged 密码已修改。
	RevokeReasonPasswordChanged RevokeReason = "password_changed"
	// RevokeReasonPhoneChanged C 端手机号换绑。
	RevokeReasonPhoneChanged RevokeReason = "phone_changed"
)

// Validate 校验撤销原因非空且在列宽内。
func (r RevokeReason) Validate() error {
	if strings.TrimSpace(string(r)) == "" {
		return errInvalid(ErrInvalidSession, "revoke reason is required")
	}
	if len(r) > MaxRevokeReasonLen {
		return errInvalid(ErrInvalidSession, "revoke reason exceeds %d bytes", MaxRevokeReasonLen)
	}
	return nil
}

// String 返回原因字面量。
func (r RevokeReason) String() string { return string(r) }

// Session 是一次登录产生的会话；刷新时在同一 FamilyID 下轮换。
//
// 不变量：
//   - RefreshHash 全局唯一且只存哈希；
//   - active → rotated 只能发生一次；rotated 再次出现视为重放；
//   - revoked / expired 永不复活。
type Session struct {
	// ID 雪花 ID。
	ID string
	// AccountID 所属账号。
	AccountID string
	// FamilyID 同一登录链的家庭 ID，重放时按此整链撤销。
	FamilyID string
	// RefreshHash 刷新令牌 SHA-256 摘要，全局唯一。
	RefreshHash string
	// Status 会话状态。
	Status SessionStatus
	// TokenVersion 签发时的账号撤销版本；用于排查跨版本会话。
	TokenVersion int64
	// Version 乐观锁版本，刷新并发时只有一个请求成功。
	Version int64
	// ExpiresAt 绝对过期时间（UTC）。
	ExpiresAt time.Time
	// IdleExpiresAt 空闲上限（UTC）；零值表示不限制，但从不超过 ExpiresAt。
	IdleExpiresAt time.Time
	// LastUsedAt 最近一次刷新时间（UTC）。
	LastUsedAt time.Time
	// RevokedAt 撤销时间；nil 表示未撤销。
	RevokedAt *time.Time
	// RevokedReason 撤销原因。
	RevokedReason RevokeReason
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewSessionParams 创建会话的入参。
type NewSessionParams struct {
	ID           string
	AccountID    string
	FamilyID     string
	RefreshHash  string
	TokenVersion int64
	ExpiresAt    time.Time
	// IdleExpiresAt 可选；零值表示不限制。
	IdleExpiresAt time.Time
	// Now 创建时间；零值时取当前 UTC 时间。
	Now time.Time
}

// NewSession 创建 active 会话。
func NewSession(p NewSessionParams) (*Session, error) {
	if strings.TrimSpace(p.ID) == "" {
		return nil, errInvalid(ErrEmptyInput, "session id is required")
	}
	if strings.TrimSpace(p.AccountID) == "" {
		return nil, errInvalid(ErrEmptyInput, "session account id is required")
	}
	if strings.TrimSpace(p.FamilyID) == "" {
		return nil, errInvalid(ErrEmptyInput, "session family id is required")
	}
	if strings.TrimSpace(p.RefreshHash) == "" {
		return nil, errInvalid(ErrEmptyInput, "refresh token hash is required")
	}
	if len(p.RefreshHash) > MaxRefreshHashLen {
		return nil, errInvalid(ErrInvalidSession, "refresh token hash exceeds %d bytes", MaxRefreshHashLen)
	}
	if p.TokenVersion < 1 {
		return nil, errInvalid(ErrInvalidSession, "token version must be >= 1")
	}
	now := utcNow(p.Now)
	expiresAt := utcNow(p.ExpiresAt)
	if !expiresAt.After(now) {
		return nil, errInvalid(ErrInvalidSession, "session expiry must be after now")
	}
	var idleExpiresAt time.Time
	if !p.IdleExpiresAt.IsZero() {
		idleExpiresAt = utcNow(p.IdleExpiresAt)
		if !idleExpiresAt.After(now) {
			return nil, errInvalid(ErrInvalidSession, "idle expiry must be after now")
		}
		if idleExpiresAt.After(expiresAt) {
			return nil, errInvalid(ErrInvalidSession, "idle expiry must not exceed session expiry")
		}
	}
	return &Session{
		ID:            p.ID,
		AccountID:     p.AccountID,
		FamilyID:      p.FamilyID,
		RefreshHash:   p.RefreshHash,
		Status:        SessionStatusActive,
		TokenVersion:  p.TokenVersion,
		Version:       1,
		ExpiresAt:     expiresAt,
		IdleExpiresAt: idleExpiresAt,
		LastUsedAt:    now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// IsActive 报告会话在 now 时刻是否可用于刷新。
func (s *Session) IsActive(now time.Time) bool {
	if s.Status != SessionStatusActive {
		return false
	}
	if !now.Before(s.ExpiresAt) {
		return false
	}
	if !s.IdleExpiresAt.IsZero() && !now.Before(s.IdleExpiresAt) {
		return false
	}
	return true
}

// Rotate 把 active 会话标记为 rotated；调用方须在同一事务内创建同 FamilyID 的新会话。
func (s *Session) Rotate(now time.Time) error {
	if s.Status == SessionStatusRotated {
		return errConflict(ErrSessionNotActive, "session %s has already been rotated", s.ID)
	}
	if s.Status != SessionStatusActive {
		return errConflict(ErrSessionNotActive, "session %s is %s", s.ID, s.Status)
	}
	if !now.Before(s.ExpiresAt) || (!s.IdleExpiresAt.IsZero() && !now.Before(s.IdleExpiresAt)) {
		return errConflict(ErrSessionExpired, "session %s has expired", s.ID)
	}
	s.Status = SessionStatusRotated
	s.LastUsedAt = utcNow(now)
	s.touch(now)
	return nil
}

// Revoke 撤销会话；active 与 rotated 可撤销，revoked 幂等。
//
// 重放检测需要撤销整条家庭链，其中必然包含已 rotated 的会话，
// 因此这里允许 rotated → revoked；expired 会话不可再变为 revoked（历史事实不再改写）。
func (s *Session) Revoke(reason RevokeReason, now time.Time) error {
	if err := reason.Validate(); err != nil {
		return err
	}
	switch s.Status {
	case SessionStatusRevoked:
		return nil
	case SessionStatusActive, SessionStatusRotated:
		revokedAt := utcNow(now)
		s.Status = SessionStatusRevoked
		s.RevokedAt = &revokedAt
		s.RevokedReason = reason
		s.touch(now)
		return nil
	default:
		return errConflict(ErrSessionExpired, "session %s is %s and cannot be revoked", s.ID, s.Status)
	}
}

// Expire 把到期会话标记为 expired；未到期返回冲突。
func (s *Session) Expire(now time.Time) error {
	if s.Status == SessionStatusExpired {
		return nil
	}
	if s.Status != SessionStatusActive && s.Status != SessionStatusRotated {
		return errConflict(ErrInvalidSessionStatus, "session %s is %s", s.ID, s.Status)
	}
	if now.Before(s.ExpiresAt) && (s.IdleExpiresAt.IsZero() || now.Before(s.IdleExpiresAt)) {
		return errConflict(ErrInvalidSessionStatus, "session %s is not expired yet", s.ID)
	}
	s.Status = SessionStatusExpired
	s.touch(now)
	return nil
}

// Touch 记录一次成功使用；仅 active 且未过期的会话可更新。
func (s *Session) Touch(now time.Time) error {
	if !s.IsActive(now) {
		return errConflict(ErrSessionNotActive, "session %s is not active", s.ID)
	}
	s.LastUsedAt = utcNow(now)
	s.touch(now)
	return nil
}

// touch 更新 UpdatedAt 并递增乐观锁版本。
func (s *Session) touch(now time.Time) {
	s.UpdatedAt = utcNow(now)
	s.Version++
}
