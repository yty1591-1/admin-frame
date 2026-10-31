package domain

import (
	"strings"
	"time"
)

// 凭据字段上限，与 migrations/000001 的列宽一致。
const (
	// MaxCredentialHashLen 编码后哈希最大字节数（Argon2id / bcrypt 均远小于此值）。
	MaxCredentialHashLen = 255
	// MaxCredentialAlgorithmLen 算法标识最大字节数。
	MaxCredentialAlgorithmLen = 32
)

// CredentialType 凭据类型。同一账号同一类型仅保留一条有效凭据。
type CredentialType string

const (
	// CredentialTypePassword 密码凭据，只存 Argon2id / bcrypt 编码哈希。
	CredentialTypePassword CredentialType = "password"
	// CredentialTypePhone 手机号绑定；库内只存不可逆摘要，供 C 端验证码登录定位账号。
	CredentialTypePhone CredentialType = "phone"
)

// Validate 校验凭据类型。
func (t CredentialType) Validate() error {
	switch t {
	case CredentialTypePassword, CredentialTypePhone:
		return nil
	default:
		return errInvalid(ErrInvalidCredentialType, "credential type %q is not supported", string(t))
	}
}

// String 返回凭据类型字面量。
func (t CredentialType) String() string { return string(t) }

// Credential 是账号的凭据实体，只保存哈希，不保存明文。
type Credential struct {
	// ID 雪花 ID。
	ID string
	// AccountID 所属账号。
	AccountID string
	// Type 凭据类型。
	Type CredentialType
	// Hash 编码后的哈希；密码使用 Argon2id / bcrypt 编码串，手机号使用不可逆摘要。
	Hash string
	// Algorithm 哈希算法标识（password 为 argon2id / bcrypt；摘要类为 hmac-sha256 / sha256）。
	Algorithm string
	// PasswordHistory 最近若干次密码哈希；仅 password 凭据使用，不含明文（SEC-06）。
	PasswordHistory []PasswordHistoryEntry
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PasswordHistoryEntry 是一条历史密码哈希记录。
type PasswordHistoryEntry struct {
	// Hash 编码后的历史哈希。
	Hash string `json:"hash"`
	// Algorithm 历史哈希算法。
	Algorithm string `json:"algorithm"`
	// ChangedAt 该哈希退出使用的时间（UTC）。
	ChangedAt time.Time `json:"changed_at"`
}

// NewCredentialParams 创建凭据的入参。
type NewCredentialParams struct {
	ID        string
	AccountID string
	Type      CredentialType
	Hash      string
	Algorithm string
	// Now 创建时间；零值时取当前 UTC 时间。
	Now time.Time
}

// NewCredential 创建凭据。
func NewCredential(p NewCredentialParams) (*Credential, error) {
	if strings.TrimSpace(p.ID) == "" {
		return nil, errInvalid(ErrEmptyInput, "credential id is required")
	}
	if strings.TrimSpace(p.AccountID) == "" {
		return nil, errInvalid(ErrEmptyInput, "credential account id is required")
	}
	if err := p.Type.Validate(); err != nil {
		return nil, err
	}
	if err := validateCredentialHash(p.Hash, p.Algorithm); err != nil {
		return nil, err
	}
	now := utcNow(p.Now)
	return &Credential{
		ID:        p.ID,
		AccountID: p.AccountID,
		Type:      p.Type,
		Hash:      p.Hash,
		Algorithm: p.Algorithm,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ReplaceHash 替换凭据哈希（改密 / 换绑手机号）。
func (c *Credential) ReplaceHash(hash, algorithm string, now time.Time) error {
	if err := validateCredentialHash(hash, algorithm); err != nil {
		return err
	}
	c.Hash = hash
	c.Algorithm = algorithm
	c.UpdatedAt = utcNow(now)
	return nil
}

// RecordPasswordChange 把旧密码哈希加入历史，并按数量与窗口裁剪（SEC-06）。
//
// previousHash 为空表示首次设置密码；retain ≤0 表示不保留历史。
func (c *Credential) RecordPasswordChange(previousHash, previousAlgorithm string, now time.Time, retain int, window time.Duration) {
	if strings.TrimSpace(previousHash) != "" {
		c.PasswordHistory = append(c.PasswordHistory, PasswordHistoryEntry{
			Hash:      previousHash,
			Algorithm: previousAlgorithm,
			ChangedAt: utcNow(now),
		})
	}
	c.prunePasswordHistory(now, retain, window)
}

// RecentPasswordHistory 返回窗口内可用于复用校验的历史哈希。
func (c *Credential) RecentPasswordHistory(now time.Time, window time.Duration) []PasswordHistoryEntry {
	if window <= 0 {
		return nil
	}
	cutoff := utcNow(now).Add(-window)
	out := make([]PasswordHistoryEntry, 0, len(c.PasswordHistory))
	for _, entry := range c.PasswordHistory {
		if entry.Hash == "" {
			continue
		}
		if entry.ChangedAt.Before(cutoff) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// prunePasswordHistory 按时间窗口与保留条数裁剪，仅保留最新记录。
func (c *Credential) prunePasswordHistory(now time.Time, retain int, window time.Duration) {
	if retain <= 0 {
		c.PasswordHistory = nil
		return
	}
	cutoff := time.Time{}
	if window > 0 {
		cutoff = utcNow(now).Add(-window)
	}
	kept := make([]PasswordHistoryEntry, 0, len(c.PasswordHistory))
	for _, entry := range c.PasswordHistory {
		if entry.Hash == "" {
			continue
		}
		if !cutoff.IsZero() && entry.ChangedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) > retain {
		kept = kept[len(kept)-retain:]
	}
	c.PasswordHistory = kept
}

// validateCredentialHash 校验哈希与算法标识非空且在列宽内。
func validateCredentialHash(hash, algorithm string) error {
	if strings.TrimSpace(hash) == "" {
		return errInvalid(ErrInvalidCredential, "credential hash is required")
	}
	if len(hash) > MaxCredentialHashLen {
		return errInvalid(ErrInvalidCredential, "credential hash exceeds %d bytes", MaxCredentialHashLen)
	}
	if strings.TrimSpace(algorithm) == "" {
		return errInvalid(ErrInvalidCredential, "credential algorithm is required")
	}
	if len(algorithm) > MaxCredentialAlgorithmLen {
		return errInvalid(ErrInvalidCredential, "credential algorithm exceeds %d bytes", MaxCredentialAlgorithmLen)
	}
	return nil
}
