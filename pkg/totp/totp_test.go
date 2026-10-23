package totp

import (
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

func TestValidateWindow(t *testing.T) {
	now := time.Unix(1111111111, 0).UTC()
	current := hotp(rfcSecret, uint64(now.Unix()/30))
	previous := hotp(rfcSecret, uint64(now.Unix()/30)-1)
	next := hotp(rfcSecret, uint64(now.Unix()/30)+1)
	outside := hotp(rfcSecret, uint64(now.Unix()/30)+2)

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
	if string(secret) == string(another) {
		t.Fatal("two generated secrets must differ")
	}
}

func TestEncodeSecretAndProvisioningURI(t *testing.T) {
	encoded := EncodeSecret(rfcSecret)
	if strings.Contains(encoded, "=") {
		t.Fatalf("encoded secret %q must not contain padding", encoded)
	}
	if decoded, err := base32NoPadding.DecodeString(encoded); err != nil || string(decoded) != string(rfcSecret) {
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
