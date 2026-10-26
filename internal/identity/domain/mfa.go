package domain

import (
	"crypto/subtle"
	"strings"
	"time"
)

// 恢复码与密文上限，与 migrations/000001 的列宽一致。
const (
	// MaxMFASecretLen TOTP 密钥密文最大字节数（AES-GCM 加密后）。
	MaxMFASecretLen = 256
	// MaxRecoveryCodeHashLen 单条恢复码哈希最大字节数。
	MaxRecoveryCodeHashLen = 128
)

// MFAEnrollment 是账号的 TOTP 绑定。
//
// 不变量：
//   - 未确认（ConfirmedAt 为空）的绑定不得用于登录；
//   - 密钥只保存 AES-GCM 密文，恢复码只保存哈希且一次性使用。
type MFAEnrollment struct {
	// AccountID 账号 ID，主键即账号。
	AccountID string
	// SecretCiphertext TOTP 密钥密文；禁止明文落库。
	SecretCiphertext []byte
	// ConfirmedAt 首次校验通过时间；nil 表示未确认。
	ConfirmedAt *time.Time
	// RecoveryCodeHashes 恢复码哈希，永不为 nil。
	RecoveryCodeHashes []string
	// CreatedAt / UpdatedAt UTC。
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewMFAEnrollment 创建未确认的 TOTP 绑定。
func NewMFAEnrollment(accountID string, secret []byte, now time.Time) (*MFAEnrollment, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, errInvalid(ErrEmptyInput, "mfa account id is required")
	}
	if len(secret) == 0 {
		return nil, errInvalid(ErrInvalidMFA, "mfa secret is required")
	}
	if len(secret) > MaxMFASecretLen {
		return nil, errInvalid(ErrInvalidMFA, "mfa secret exceeds %d bytes", MaxMFASecretLen)
	}
	createdAt := utcNow(now)
	return &MFAEnrollment{
		AccountID:          accountID,
		SecretCiphertext:   append([]byte(nil), secret...),
		RecoveryCodeHashes: []string{},
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
	}, nil
}

// IsConfirmed 报告绑定是否已通过首个动态码校验。
func (m *MFAEnrollment) IsConfirmed() bool { return m.ConfirmedAt != nil }

// Confirm 标记绑定完成；重复确认返回冲突，避免重放 setup 请求覆盖状态。
func (m *MFAEnrollment) Confirm(now time.Time) error {
	if m.IsConfirmed() {
		return errConflict(ErrMFAAlreadyConfirmed, "mfa enrollment for account %s is already confirmed", m.AccountID)
	}
	confirmedAt := utcNow(now)
	m.ConfirmedAt = &confirmedAt
	m.UpdatedAt = confirmedAt
	return nil
}

// SetRecoveryCodes 设置恢复码哈希集合（至少一条，逐条非空）。
func (m *MFAEnrollment) SetRecoveryCodes(hashes []string, now time.Time) error {
	if len(hashes) == 0 {
		return errInvalid(ErrInvalidMFA, "at least one recovery code hash is required")
	}
	next := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		if strings.TrimSpace(hash) == "" {
			return errInvalid(ErrInvalidMFA, "recovery code hash must not be empty")
		}
		if len(hash) > MaxRecoveryCodeHashLen {
			return errInvalid(ErrInvalidMFA, "recovery code hash exceeds %d bytes", MaxRecoveryCodeHashLen)
		}
		next = append(next, hash)
	}
	m.RecoveryCodeHashes = next
	m.UpdatedAt = utcNow(now)
	return nil
}

// HasRecoveryCode 报告恢复码哈希是否仍可使用；比较使用常量时间。
func (m *MFAEnrollment) HasRecoveryCode(hash string) bool {
	target := strings.TrimSpace(hash)
	if target == "" {
		return false
	}
	for _, candidate := range m.RecoveryCodeHashes {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(target)) == 1 {
			return true
		}
	}
	return false
}

// UseRecoveryCode 一次性消费恢复码哈希；成功时移除该哈希并刷新更新时间。
//
// 恢复码只允许使用一次，重复使用必须返回 false，由调用方按校验失败处理。
func (m *MFAEnrollment) UseRecoveryCode(hash string, now time.Time) bool {
	target := strings.TrimSpace(hash)
	if target == "" {
		return false
	}
	for i, candidate := range m.RecoveryCodeHashes {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(target)) != 1 {
			continue
		}
		m.RecoveryCodeHashes = append(m.RecoveryCodeHashes[:i], m.RecoveryCodeHashes[i+1:]...)
		if m.RecoveryCodeHashes == nil {
			m.RecoveryCodeHashes = []string{}
		}
		m.UpdatedAt = utcNow(now)
		return true
	}
	return false
}
