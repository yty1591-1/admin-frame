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

// Policy 是 TOTP 可配置参数（MNT-05）；零值字段回退默认值。
type Policy struct {
	// Digits 动态码位数，仅支持 6 或 8。
	Digits int
	// Period 时间步长。
	Period time.Duration
	// Window 允许的窗口偏移（±Window）。
	Window int
}

// DefaultPolicy 返回 RFC 6238 默认参数：6 位 / 30s / ±1。
func DefaultPolicy() Policy {
	return Policy{Digits: Digits, Period: Period, Window: Window}
}

// Normalize 用默认值补齐零值字段。
func (p Policy) Normalize() Policy {
	out := p
	if out.Digits == 0 {
		out.Digits = Digits
	}
	if out.Period <= 0 {
		out.Period = Period
	}
	// 0 表示未配置，使用默认 ±1。
	if out.Window <= 0 {
		out.Window = Window
	}
	return out
}

// Validate 校验参数边界：位数 6/8，步长 [15s, 120s]，窗口 [0, 2]。
func (p Policy) Validate() error {
	normalized := p.Normalize()
	if normalized.Digits != 6 && normalized.Digits != 8 {
		return fmt.Errorf("totp: digits must be 6 or 8")
	}
	if normalized.Period < 15*time.Second || normalized.Period > 120*time.Second {
		return fmt.Errorf("totp: period must be within [15s, 120s]")
	}
	if normalized.Window > 2 {
		return fmt.Errorf("totp: window must be within [0, 2]")
	}
	return nil
}

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
	_, ok := ValidateStep(secret, code, now)
	return ok
}

// ValidateStep 校验动态码并返回命中的时间步；用于服务端防重放（IDN-P1-05）。
//
// 命中多个窗口时返回最大的时间步（最接近未来的窗口）。
func ValidateStep(secret []byte, code string, now time.Time) (int64, bool) {
	return ValidateStepWith(secret, code, now, DefaultPolicy())
}

// ValidateStepWith 使用指定参数校验动态码（MNT-05）。
func ValidateStepWith(secret []byte, code string, now time.Time, policy Policy) (int64, bool) {
	policy = policy.Normalize()
	if len(secret) == 0 {
		return 0, false
	}
	normalized := strings.TrimSpace(code)
	if len(normalized) != policy.Digits {
		return 0, false
	}
	for _, r := range normalized {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	stepSeconds := int64(policy.Period / time.Second)
	if stepSeconds <= 0 {
		return 0, false
	}
	seconds := now.UTC().Unix() / stepSeconds
	var matched int64 = -1
	for offset := -policy.Window; offset <= policy.Window; offset++ {
		candidateSeconds := seconds + int64(offset)
		// 计数器为无符号数：负偏移在 Unix 纪元附近会下溢，直接跳过。
		if candidateSeconds < 0 {
			continue
		}
		candidate := hotp(secret, uint64(candidateSeconds), policy.Digits)
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(normalized)) == 1 {
			matched = candidateSeconds
		}
	}
	if matched < 0 {
		return 0, false
	}
	return matched, true
}

// ProvisioningURI 生成 otpauth://totp URI，供客户端扫码录入。
func ProvisioningURI(issuer, account, secretBase32 string) string {
	return ProvisioningURIWith(issuer, account, secretBase32, DefaultPolicy())
}

// ProvisioningURIWith 使用指定参数生成 otpauth://totp URI（MNT-05）。
func ProvisioningURIWith(issuer, account, secretBase32 string, policy Policy) string {
	policy = policy.Normalize()
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
	query.Set("digits", fmt.Sprintf("%d", policy.Digits))
	query.Set("period", fmt.Sprintf("%d", int64(policy.Period/time.Second)))
	return "otpauth://totp/" + url.PathEscape(label) + "?" + query.Encode()
}

// hotp 计算指定计数器的 HOTP 动态码（RFC 4226 动态截断）。
func hotp(secret []byte, counter uint64, digits int) string {
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
	modulus := uint32(1_000_000)
	if digits == 8 {
		modulus = 100_000_000
	}
	return fmt.Sprintf("%0*d", digits, value%modulus)
}
