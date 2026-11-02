package totp

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// rfcSecret 是 RFC 6238 使用的 ASCII 测试密钥。
var rfcSecret = []byte("12345678901234567890")

func TestValidateRFCVectors(t *testing.T) {
	vectors := []struct {
		name string
		unix int64
		want string
	}{
		{"epoch", 59, "287082"},
		{"2005-03-18 01:58:29", 1111111109, "081804"},
		{"2005-03-18 01:58:31", 1111111111, "050471"},
		{"2009-02-13 23:31:30", 1234567890, "005924"},
		{"2033-05-18 03:33:20", 2000000000, "279037"},
		{"2603-10-11 11:33:20", 20000000000, "353130"},
	}
	for _, tc := range vectors {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(tc.unix, 0).UTC()
			if !Validate(rfcSecret, tc.want, now) {
				t.Fatalf("Validate(%q) = false, want true", tc.want)
			}
		})
	}
}

// TestValidateStepWithPolicy 验证可配置位数 / 步长 / 窗口（MNT-05）。
func TestValidateStepWithPolicy(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	policy := Policy{Digits: 8, Period: 60 * time.Second, Window: 1}
	counter := uint64(now.Unix() / 60) //nolint:gosec // G115: 测试时间戳固定为正
	code := hotp(rfcSecret, counter, policy.Digits)
	if len(code) != 8 {
		t.Fatalf("code length = %d, want 8", len(code))
	}
	step, ok := ValidateStepWith(rfcSecret, code, now, policy)
	if !ok || step != int64(counter) { //nolint:gosec // G115: counter 来自固定测试时间戳，远小于 int64 上限
		t.Fatalf("ValidateStepWith() = (%d, %v), want (%d, true)", step, ok, counter)
	}
	// 8 位策略下 6 位动态码必须被拒绝。
	if _, ok := ValidateStepWith(rfcSecret, code[:6], now, policy); ok {
		t.Fatal("6-digit code must be rejected by 8-digit policy")
	}
	uri := ProvisioningURIWith("Issuer", "alice", "JBSWY3DPEHPK3PXP", policy)
	if !strings.Contains(uri, "digits=8") || !strings.Contains(uri, "period=60") {
		t.Fatalf("provisioning uri = %q, want digits=8 period=60", uri)
	}
}

func TestValidateWindow(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	counter := uint64(now.Unix() / 30) //nolint:gosec // G115: 测试时间戳固定为正，不存在溢出
	current := hotp(rfcSecret, counter, Digits)
	previous := hotp(rfcSecret, counter-1, Digits)
	next := hotp(rfcSecret, counter+1, Digits)
	outside := hotp(rfcSecret, counter+2, Digits)

	if !Validate(rfcSecret, current, now) {
		t.Fatal("current window code should be valid")
	}
	if !Validate(rfcSecret, previous, now) {
		t.Fatal("previous window code should be valid")
	}
	if !Validate(rfcSecret, next, now) {
		t.Fatal("next window code should be valid")
	}
	if Validate(rfcSecret, outside, now) {
		t.Fatal("code outside ±1 window should be rejected")
	}
}

func TestValidateRejectsMalformed(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	cases := []struct {
		name   string
		secret []byte
		code   string
	}{
		{"empty secret", nil, "050471"},
		{"empty code", rfcSecret, ""},
		{"too short", rfcSecret, "12345"},
		{"too long", rfcSecret, "1234567"},
		{"non digit", rfcSecret, "12a456"},
		{"spaces inside", rfcSecret, "12 456"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if Validate(tc.secret, tc.code, now) {
				t.Fatalf("Validate(%q) = true, want false", tc.code)
			}
		})
	}
}

func TestGenerateSecret(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret() error = %v", err)
	}
	if len(secret) != SecretBytes {
		t.Fatalf("len(secret) = %d, want %d", len(secret), SecretBytes)
	}
	another, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret() error = %v", err)
	}
	if bytes.Equal(secret, another) {
		t.Fatal("two generated secrets must differ")
	}
}

func TestEncodeSecretAndProvisioningURI(t *testing.T) {
	encoded := EncodeSecret(rfcSecret)
	if strings.Contains(encoded, "=") {
		t.Fatalf("encoded secret %q must not contain padding", encoded)
	}
	if decoded, err := base32NoPadding.DecodeString(encoded); err != nil || !bytes.Equal(decoded, rfcSecret) {
		t.Fatalf("encoded secret is not decodable: %q, err = %v", encoded, err)
	}

	uri := ProvisioningURI("Admin Frame", "alice@example.com", encoded)
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("uri = %q, want otpauth scheme", uri)
	}
	if !strings.Contains(uri, "secret="+encoded) {
		t.Fatalf("uri = %q, want secret query", uri)
	}
	if !strings.Contains(uri, "issuer=Admin+Frame") {
		t.Fatalf("uri = %q, want encoded issuer", uri)
	}
	if !strings.Contains(uri, "digits=6") || !strings.Contains(uri, "period=30") {
		t.Fatalf("uri = %q, want digits/period", uri)
	}
}
