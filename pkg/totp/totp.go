package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 规定 TOTP 使用 HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP 参数（docs/identity-authn.md §7.1）。
const (
	// SecretBytes 密钥随机字节数（20 字节 = 160 bit）。
	SecretBytes = 20
	// Digits 动态码位数。
	Digits = 6
	// Period 时间步长。
	Period = 30 * time.Second
	// Window 允许的窗口偏移（±1）。
	Window = 1
)

// base32NoPadding 是 otpauth 使用的 Base32 编码（无填充、大写）。
var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret 生成 20 字节密码学随机密钥。
func GenerateSecret() ([]byte, error) {
	secret := make([]byte, SecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("totp: generate secret: %w", err)
	}
	return secret, nil
}

// EncodeSecret 把密钥编码为无填充 Base32 字符串，供客户端录入。
func EncodeSecret(secret []byte) string {
	return base32NoPadding.EncodeToString(secret)
}

// Validate 校验动态码是否命中当前时间 ±1 个窗口。
//
// code 必须为 6 位数字；任何格式错误都返回 false，调用方不得据此区分失败原因。
func Validate(secret []byte, code string, now time.Time) bool {
	if len(secret) == 0 {
		return false
	}
	normalized := strings.TrimSpace(code)
	if len(normalized) != Digits {
		return false
	}
	for _, r := range normalized {
		if r < '0' || r > '9' {
			return false
		}
	}
	seconds := now.UTC().Unix() / int64(Period/time.Second)
	for offset := -Window; offset <= Window; offset++ {
		candidateSeconds := seconds + int64(offset)
		// 计数器为无符号数：负偏移在 Unix 纪元附近会下溢，直接跳过。
		if candidateSeconds < 0 {
			continue
		}
		candidate := hotp(secret, uint64(candidateSeconds))
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(normalized)) == 1 {
			return true
		}
	}
	return false
}

// ProvisioningURI 生成 otpauth://totp URI，供客户端扫码录入。
func ProvisioningURI(issuer, account, secretBase32 string) string {
	label := strings.TrimSpace(account)
	if trimmedIssuer := strings.TrimSpace(issuer); trimmedIssuer != "" {
		label = trimmedIssuer + ":" + label
	}
	query := url.Values{}
	query.Set("secret", secretBase32)
	if issuer = strings.TrimSpace(issuer); issuer != "" {
		query.Set("issuer", issuer)
	}
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprintf("%d", Digits))
	query.Set("period", fmt.Sprintf("%d", int64(Period/time.Second)))
	return "otpauth://totp/" + url.PathEscape(label) + "?" + query.Encode()
}

// hotp 计算指定计数器的 HOTP 动态码（RFC 4226 动态截断）。
func hotp(secret []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
