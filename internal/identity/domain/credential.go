package domain

import (
	"strings"
	"time"
)

// 凭据字段上限，与 migrations/000011 的列宽一致。
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
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
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
