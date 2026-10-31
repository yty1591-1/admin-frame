package persistence

import (
	"bytes"
	"testing"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// newTestProtector 构造固定密钥的保护器，便于断言确定性与轮换。
func newTestProtector(t *testing.T) SubjectProtector {
	t.Helper()
	hashKeys := map[string][]byte{"v1": bytes.Repeat([]byte{0x01}, 32)}
	hasher, err := crypto.NewHasher("v1", hashKeys)
	if err != nil {
		t.Fatalf("new hasher: %v", err)
	}
	ring, err := crypto.NewKeyRing(map[string][]byte{"k1": bytes.Repeat([]byte{0x02}, 32)}, "k1", nil)
	if err != nil {
		t.Fatalf("new key ring: %v", err)
	}
	t.Cleanup(ring.Close)
	return NewSubjectProtector(hasher, ring)
}

// TestSubjectIndexDeterministicAndTenantIsolated 验证索引确定性与租户隔离。
func TestSubjectIndexDeterministicAndTenantIsolated(t *testing.T) {
	protector := newTestProtector(t)
	first := protector.Index(domain.ChannelBiz, "merchant-a", "  Alice@Example.COM ")
	second := protector.Index(domain.ChannelBiz, "merchant-a", "alice@example.com")
	if first != second {
		t.Fatalf("index must normalize case and whitespace: %q vs %q", first, second)
	}
	otherTenant := protector.Index(domain.ChannelBiz, "merchant-b", "alice@example.com")
	if first == otherTenant {
		t.Fatalf("index must differ across merchants, got %q", first)
	}
	otherChannel := protector.Index(domain.ChannelAdmin, "merchant-a", "alice@example.com")
	if first == otherChannel {
		t.Fatalf("index must differ across channels, got %q", first)
	}
}

// TestSubjectEncryptRoundTripAndAAD 验证密文可解密且 AAD 绑定账号。
func TestSubjectEncryptRoundTripAndAAD(t *testing.T) {
	protector := newTestProtector(t)
	ciphertext, err := protector.Encrypt("1001", "alice@example.com")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("alice@example.com")) {
		t.Fatalf("ciphertext must not contain plaintext")
	}
	plaintext, err := protector.Decrypt("1001", ciphertext)
	if err != nil || plaintext != "alice@example.com" {
		t.Fatalf("decrypt = %q, err=%v", plaintext, err)
	}
	if _, err := protector.Decrypt("1002", ciphertext); err == nil {
		t.Fatalf("decrypt with wrong account id must fail (AAD binding)")
	}
}

// TestProviderSubjectProtection 验证外部身份主体索引确定性与密文 AAD 绑定。
func TestProviderSubjectProtection(t *testing.T) {
	protector := newTestProtector(t)
	first := protector.ProviderIndex("github", "sub-1")
	if first == "" || first != protector.ProviderIndex("github", "sub-1") {
		t.Fatalf("provider index must be deterministic, got %q", first)
	}
	if first == protector.ProviderIndex("oidc-corp", "sub-1") {
		t.Fatalf("provider index must differ across providers, got %q", first)
	}
	ciphertext, err := protector.EncryptProviderSubject("3001", "sub-1")
	if err != nil {
		t.Fatalf("encrypt provider subject: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("sub-1")) {
		t.Fatalf("provider ciphertext must not contain plaintext")
	}
	plaintext, err := protector.DecryptProviderSubject("3001", ciphertext)
	if err != nil || plaintext != "sub-1" {
		t.Fatalf("decrypt provider subject = %q, err=%v", plaintext, err)
	}
	if _, err := protector.DecryptProviderSubject("3002", ciphertext); err == nil {
		t.Fatalf("decrypt with wrong identity id must fail (AAD binding)")
	}
}

// TestSubjectDecryptAfterKeyRotation 验证旧密钥密文在主密钥轮换后仍可读。
func TestSubjectDecryptAfterKeyRotation(t *testing.T) {
	oldRing, err := crypto.NewKeyRing(map[string][]byte{"k1": bytes.Repeat([]byte{0x02}, 32)}, "k1", nil)
	if err != nil {
		t.Fatalf("old ring: %v", err)
	}
	ciphertext, err := oldRing.Seal([]byte("bob@example.com"), subjectAAD("2001"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	oldRing.Close()

	newRing, err := crypto.NewKeyRing(map[string][]byte{
		"k1": bytes.Repeat([]byte{0x02}, 32),
		"k2": bytes.Repeat([]byte{0x03}, 32),
	}, "k2", nil)
	if err != nil {
		t.Fatalf("new ring: %v", err)
	}
	defer newRing.Close()
	hasher, err := crypto.NewHasher("v1", map[string][]byte{"v1": bytes.Repeat([]byte{0x01}, 32)})
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	protector := NewSubjectProtector(hasher, newRing)
	plaintext, err := protector.Decrypt("2001", ciphertext)
	if err != nil || plaintext != "bob@example.com" {
		t.Fatalf("rotation decrypt = %q, err=%v", plaintext, err)
	}
}
