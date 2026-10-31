package persistence

import (
	"fmt"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// subjectIndexDomain 是确定性索引的 HMAC 域分隔前缀。
//
// 域内再拼接端别与商户，保证同一主体在不同租户下得到不同索引，
// 避免跨租户索引碰撞；与 docs/identity-p1-remaining-implementation-plan.md §4.2 一致。
const subjectIndexDomain = "account-subject"

// SubjectProtector 是登录主体的确定性索引与可逆加密端口（IDN-GAP-P1-06）。
//
// 约定：
//   - Index 必须确定性且大小写 / 空白归一化，供唯一键与登录查找使用；
//   - Encrypt / Decrypt 的 AAD 必须绑定 account_id，防止密文跨账号搬运；
//   - 实现必须并发安全、无状态，可由多个仓储共享。
type SubjectProtector interface {
	// Index 计算 (channel, merchant_id, subject) 的确定性索引。
	Index(channel domain.Channel, merchantID, subject string) string
	// Encrypt 使用 active key 加密 subject，AAD 绑定 accountID。
	Encrypt(accountID, subject string) ([]byte, error)
	// Decrypt 按密文头选择密钥解密；支持密钥轮换双读。
	Decrypt(accountID string, ciphertext []byte) (string, error)
}

// subjectCrypto 组合 HMAC 摘要器与 AES-256-GCM 密钥环。
type subjectCrypto struct {
	hasher *crypto.Hasher
	ring   *crypto.KeyRing
}

// 编译期断言。
var _ SubjectProtector = (*subjectCrypto)(nil)

// NewSubjectProtector 构造主体保护器；任一依赖缺失时返回 nil（表示未启用 S1/S2 双写）。
func NewSubjectProtector(hasher *crypto.Hasher, ring *crypto.KeyRing) SubjectProtector {
	if hasher == nil || ring == nil {
		return nil
	}
	return &subjectCrypto{hasher: hasher, ring: ring}
}

// Index 计算确定性索引；未注入保护器时返回空串。
func (p *subjectCrypto) Index(channel domain.Channel, merchantID, subject string) string {
	if p == nil {
		return ""
	}
	domainPart := fmt.Sprintf("%s|%s|%s", subjectIndexDomain, channel.String(), merchantID)
	return p.hasher.Digest(domainPart, domain.NormalizeSubject(subject))
}

// Encrypt 加密登录主体，AAD 绑定 accountID。
func (p *subjectCrypto) Encrypt(accountID, subject string) ([]byte, error) {
	if p == nil {
		return nil, nil
	}
	ciphertext, err := p.ring.Seal([]byte(subject), subjectAAD(accountID))
	if err != nil {
		return nil, fmt.Errorf("identity subject encrypt: %w", err)
	}
	return ciphertext, nil
}

// Decrypt 解密登录主体；密文为空返回空串。
func (p *subjectCrypto) Decrypt(accountID string, ciphertext []byte) (string, error) {
	if p == nil || len(ciphertext) == 0 {
		return "", nil
	}
	plaintext, err := p.ring.Open(ciphertext, subjectAAD(accountID))
	if err != nil {
		return "", fmt.Errorf("identity subject decrypt: %w", err)
	}
	return string(plaintext), nil
}

// subjectAAD 生成绑定账号的附加认证数据。
func subjectAAD(accountID string) []byte {
	return []byte("identity:account:" + accountID)
}
