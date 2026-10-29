package crypto_test

import (
	"errors"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/crypto"
)

// TestKeyRingRoundTripAndRotation 验证按 key id 加解密、旧格式回退与轮换判定。
func TestKeyRingRoundTripAndRotation(t *testing.T) {
	keyA := []byte("01234567890123456789012345678901")
	keyB := []byte("abcdefghijklmnopqrstuvwxyz012345")
	ring, err := crypto.NewKeyRing(map[string][]byte{"a": keyA, "b": keyB}, "b", keyB)
	if err != nil {
		t.Fatalf("NewKeyRing() error = %v", err)
	}
	defer ring.Close()

	blob, err := ring.Seal([]byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if got := ring.KeyID(blob); got != "b" {
		t.Fatalf("KeyID() = %q, want b", got)
	}
	if ring.NeedsRotation(blob) {
		t.Fatalf("active key ciphertext must not need rotation")
	}
	plain, err := ring.Open(blob, []byte("aad"))
	if err != nil || string(plain) != "secret" {
		t.Fatalf("Open() = %q, %v; want secret", plain, err)
	}

	// 模拟旧格式密文：应回退 legacy 密钥并判定需要轮换。
	legacy, err := crypto.NewAES256GCM(keyB)
	if err != nil {
		t.Fatalf("legacy cipher: %v", err)
	}
	legacyBlob, err := legacy.Seal([]byte("legacy"), []byte("aad"))
	if err != nil {
		t.Fatalf("legacy Seal() error = %v", err)
	}
	if ring.KeyID(legacyBlob) != "" {
		t.Fatalf("legacy blob key id = %q, want empty", ring.KeyID(legacyBlob))
	}
	if !ring.NeedsRotation(legacyBlob) {
		t.Fatalf("legacy blob must need rotation")
	}
	plain, err = ring.Open(legacyBlob, []byte("aad"))
	if err != nil || string(plain) != "legacy" {
		t.Fatalf("legacy Open() = %q, %v", plain, err)
	}

	// 篡改 key id 必须失败关闭。
	bad := append([]byte(nil), blob...)
	bad[2] = 'z'
	if _, err := ring.Open(bad, []byte("aad")); !errors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("unknown key id err = %v, want ErrInvalidCiphertext", err)
	}
}

// TestNewKeyRingValidates 验证缺少 active key 或非法密钥时装配失败。
func TestNewKeyRingValidates(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	if _, err := crypto.NewKeyRing(nil, "a", nil); err == nil {
		t.Fatalf("empty key ring must fail")
	}
	if _, err := crypto.NewKeyRing(map[string][]byte{"a": key}, "b", nil); err == nil {
		t.Fatalf("missing active key must fail")
	}
	if _, err := crypto.NewKeyRing(map[string][]byte{"a": []byte("short")}, "a", nil); err == nil {
		t.Fatalf("invalid key length must fail")
	}
}
