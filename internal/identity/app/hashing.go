package app

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// 摘要域常量；不同域之间不可交叉复用（IDN-P1-06）。
const (
	HashDomainAccountRef   = "account-ref"
	HashDomainClientIP     = "client-ip"
	HashDomainClientUA     = "client-ua"
	HashDomainPhoneSubject = "phone-subject"
	HashDomainSMSCode      = "sms-code"
	HashDomainRecoveryCode = "recovery-code"
	HashDomainOAuthSubject = "oauth-subject"
)

// AccountRef 返回账号主体摘要；配置 hash_key 时使用带密钥 HMAC，否则回退兼容的无盐 SHA-256。
func AccountRef(hasher *crypto.Hasher, channel domain.Channel, subject string) string {
	if hasher != nil {
		return hasher.Digest(HashDomainAccountRef, string(channel)+"|"+domain.NormalizeSubject(subject))
	}
	return HashAccountRef(channel, subject)
}

// ClientRef 返回客户端 IP / UA 摘要；domain 区分 ip 与 ua，避免交叉复用。
//
// 未配置 hash_key 时保持历史无盐算法，用于本地调试与既有测试。
func ClientRef(hasher *crypto.Hasher, hashDomain, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if hasher != nil {
		return hasher.Digest(hashDomain, trimmed)
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

// PhoneRef 返回手机号主体摘要；C 端账号主体与短信限流共用该摘要。
func PhoneRef(hasher *crypto.Hasher, phone string) string {
	if hasher != nil {
		return hasher.Digest(HashDomainPhoneSubject, domain.NormalizeSubject(phone))
	}
	return HashAccountRef(domain.ChannelC, phone)
}

// SMSCodeDigest 返回短信验证码摘要；配置 hash_key 后即使 Redis 泄露也无法离线枚举。
func SMSCodeDigest(hasher *crypto.Hasher, phoneHash, code string) string {
	normalized := strings.TrimSpace(code)
	if hasher != nil {
		return hasher.Digest(HashDomainSMSCode, phoneHash+"|"+normalized)
	}
	return hashSMSCode(phoneHash, normalized)
}

// SMSCodeMatches 以常量时间校验短信验证码摘要；支持历史密钥双读。
func SMSCodeMatches(hasher *crypto.Hasher, stored, phoneHash, code string) bool {
	normalized := strings.TrimSpace(code)
	if hasher != nil {
		return hasher.Verify(stored, HashDomainSMSCode, phoneHash+"|"+normalized)
	}
	expected := hashSMSCode(phoneHash, normalized)
	return subtle.ConstantTimeCompare([]byte(stored), []byte(expected)) == 1
}

// RecoveryDigests 返回恢复码的全部候选摘要（active 优先，历史 key 用于轮换双读）。
func RecoveryDigests(hasher *crypto.Hasher, accountID, code string) []string {
	normalized := normalizeRecoveryCode(code)
	if hasher != nil {
		return hasher.Digests(HashDomainRecoveryCode, accountID+"|"+normalized)
	}
	sum := sha256.Sum256([]byte("recovery|" + accountID + "|" + normalized))
	return []string{hex.EncodeToString(sum[:])}
}

// OAuthSubjectRef 返回外部身份在身份域的登录主体。
func OAuthSubjectRef(hasher *crypto.Hasher, provider, subject string) string {
	if hasher != nil {
		return "oauth:" + provider + ":" + hasher.Digest(HashDomainOAuthSubject, provider+"|"+subject)
	}
	sum := sha256.Sum256([]byte(subject))
	return "oauth:" + provider + ":" + hex.EncodeToString(sum[:])
}
