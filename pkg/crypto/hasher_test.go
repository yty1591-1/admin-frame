package crypto_test

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// TestHasherKnownVector 使用预计算向量锁定 HMAC 输入拼接规则（domain|value）。
func TestHasherKnownVector(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	hasher, err := crypto.NewHasher("v1", map[string][]byte{"v1": key})
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	got := hasher.Digest("account-ref", "admin|alice@example.com")
	want := "v1:b25eb16ada41b53f35713e52bd4a23ac6917383668b63235d72a08dabbbf535c"
	if got != want {
		t.Fatalf("Digest() = %q, want %q", got, want)
	}
	if hasher.KeyID(got) != "v1" {
		t.Fatalf("KeyID() = %q, want v1", hasher.KeyID(got))
	}
	if !hasher.Verify(got, "account-ref", "admin|alice@example.com") {
		t.Fatalf("Verify() = false, want true")
	}
}

// TestHasherDomainSeparation 验证不同域 / 不同值不可交叉复用。
func TestHasherDomainSeparation(t *testing.T) {
	hasher, err := crypto.NewHasher("v1", map[string][]byte{"v1": []byte("01234567890123456789012345678901")})
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	ip := hasher.Digest("client-ip", "203.0.113.7")
	ua := hasher.Digest("client-ua", "203.0.113.7")
	if ip == ua {
		t.Fatalf("different domains must not share digest")
	}
	if hasher.Verify(ip, "client-ua", "203.0.113.7") {
		t.Fatalf("digest must not verify under another domain")
	}
	if !strings.HasPrefix(ip, "v1:") || len(ip) != len("v1:")+64 {
		t.Fatalf("unexpected digest format: %q", ip)
	}
}

// TestHasherRotationDualRead 验证历史 key 的摘要仍可校验，Digests 覆盖全部 key。
func TestHasherRotationDualRead(t *testing.T) {
	keyA := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	keyB := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	hasher, err := crypto.NewHasher("v2", map[string][]byte{"v1": keyA, "v2": keyB})
	if err != nil {
		t.Fatalf("NewHasher() error = %v", err)
	}
	oldDigest := "v1:" + hasherDigestForKey(keyA, "sms-code", "phone|123456")
	if !hasher.Verify(oldDigest, "sms-code", "phone|123456") {
		t.Fatalf("historical key digest must verify")
	}
	digests := hasher.Digests("recovery-code", "acct|CODE")
	if len(digests) != 2 || !strings.HasPrefix(digests[0], "v2:") {
		t.Fatalf("Digests() = %v, want active first with 2 keys", digests)
	}
}

// hasherDigestForKey 复用 Hasher 的拼接规则生成指定 key 的摘要（不含 key id）。
func hasherDigestForKey(key []byte, domain, value string) string {
	single, _ := crypto.NewHasher("k", map[string][]byte{"k": key})
	digest := single.Digest(domain, value)
	return strings.TrimPrefix(digest, "k:")
}

// TestNewHasherValidates 验证密钥长度与 active key 校验。
func TestNewHasherValidates(t *testing.T) {
	if _, err := crypto.NewHasher("", map[string][]byte{"v1": []byte("01234567890123456789012345678901")}); err == nil {
		t.Fatalf("empty active id must fail")
	}
	if _, err := crypto.NewHasher("v2", map[string][]byte{"v1": []byte("01234567890123456789012345678901")}); err == nil {
		t.Fatalf("missing active key must fail")
	}
	if _, err := crypto.NewHasher("v1", map[string][]byte{"v1": []byte("short")}); err == nil {
		t.Fatalf("invalid key length must fail")
	}
}
