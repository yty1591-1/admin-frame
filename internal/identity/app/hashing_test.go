package app_test

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// testHasher 构造固定密钥的摘要器。
func testHasher(t *testing.T) *crypto.Hasher {
	t.Helper()
	hasher, err := crypto.NewHasher("v1", map[string][]byte{
		"v1": []byte("01234567890123456789012345678901"),
	})
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	return hasher
}

// TestHashRefsKeyed 验证 PII 摘要带 key id、域隔离与验证码可校验。
func TestHashRefsKeyed(t *testing.T) {
	hasher := testHasher(t)
	first := app.AccountRef(hasher, domain.ChannelAdmin, "  Alice@Example.COM ")
	second := app.AccountRef(hasher, domain.ChannelAdmin, "alice@example.com")
	if first != second {
		t.Fatalf("normalized subjects must share digest: %q / %q", first, second)
	}
	if !strings.HasPrefix(first, "v1:") || len(first) != len("v1:")+64 {
		t.Fatalf("account ref = %q, want v1:<64 hex>", first)
	}
	if other := app.AccountRef(hasher, domain.ChannelBiz, "alice@example.com"); other == first {
		t.Fatalf("digest must differ across channels")
	}

	ip := app.ClientRef(hasher, app.HashDomainClientIP, "203.0.113.7")
	ua := app.ClientRef(hasher, app.HashDomainClientUA, "203.0.113.7")
	if ip == ua {
		t.Fatalf("ip and ua must use different domains")
	}

	phone := app.PhoneRef(hasher, "+8613800138000")
	if !strings.HasPrefix(phone, "v1:") {
		t.Fatalf("phone ref = %q, want key id prefix", phone)
	}
	codeDigest := app.SMSCodeDigest(hasher, phone, "123456")
	if !strings.HasPrefix(codeDigest, "v1:") || strings.Contains(codeDigest, "123456") {
		t.Fatalf("sms code digest = %q, must be keyed and not contain plaintext", codeDigest)
	}
	if !app.SMSCodeMatches(hasher, codeDigest, phone, "123456") {
		t.Fatalf("correct sms code must match")
	}
	if app.SMSCodeMatches(hasher, codeDigest, phone, "654321") {
		t.Fatalf("wrong sms code must not match")
	}
}

// TestHashRefsLegacyFallback 验证未配置 hash_key 时保持历史格式（仅本地/tests）。
func TestHashRefsLegacyFallback(t *testing.T) {
	if got := app.AccountRef(nil, domain.ChannelAdmin, "alice@example.com"); len(got) != 64 {
		t.Fatalf("legacy account ref len = %d, want 64", len(got))
	}
	if got := app.ClientRef(nil, app.HashDomainClientIP, "203.0.113.7"); len(got) != 64 {
		t.Fatalf("legacy client ref len = %d, want 64", len(got))
	}
	if got := app.SMSCodeDigest(nil, "phone", "123456"); len(got) != 64 {
		t.Fatalf("legacy sms digest len = %d, want 64", len(got))
	}
}
