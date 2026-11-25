package envelope_test

import (
	"bytes"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/envelope"
)

func TestSealOpenRoundTrip(t *testing.T) {
	kek, err := envelope.NewLocalKEK(bytes.Repeat([]byte{0x11}, 32), "local/test")
	if err != nil {
		t.Fatal(err)
	}
	aad := envelope.AAD("dev", "platform", "notification.smtp.password", 1)
	blob, err := envelope.Seal(kek, []byte("s3cr3t-value"), aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if blob.Algorithm != envelope.Algorithm || len(blob.Nonce) != envelope.NonceSize {
		t.Fatalf("blob meta unexpected: %+v", blob)
	}
	plain, err := envelope.Open(kek, blob, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(plain) != "s3cr3t-value" {
		t.Fatalf("plaintext = %q", plain)
	}
}

func TestOpenRejectsWrongAAD(t *testing.T) {
	kek, err := envelope.NewLocalKEK(bytes.Repeat([]byte{0x22}, 32), "local/test")
	if err != nil {
		t.Fatal(err)
	}
	aad := envelope.AAD("dev", "platform", "k.a", 1)
	blob, err := envelope.Seal(kek, []byte("plain"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envelope.Open(kek, blob, envelope.AAD("prod", "platform", "k.a", 1)); err == nil {
		t.Fatal("expected AAD mismatch failure")
	}
}

func TestRejectLocalKEKInProd(t *testing.T) {
	kek, _ := envelope.NewLocalKEK(bytes.Repeat([]byte{0x33}, 32), "local/1")
	if err := envelope.RejectLocalKEKInProd("prod", kek); err == nil {
		t.Fatal("expected rejection")
	}
	if err := envelope.RejectLocalKEKInProd("dev", kek); err != nil {
		t.Fatalf("dev should allow: %v", err)
	}
}

// TestLocalKEKRotationRewrap 模拟 M3 CC-F-55：新旧 LocalKEK 双钥窗口 unwrap → rewrap → 旧钥下线。
func TestLocalKEKRotationRewrap(t *testing.T) {
	oldKEK, err := envelope.NewLocalKEK(bytes.Repeat([]byte{0x41}, 32), "local/1")
	if err != nil {
		t.Fatal(err)
	}
	newKEK, err := envelope.NewLocalKEK(bytes.Repeat([]byte{0x42}, 32), "local/2")
	if err != nil {
		t.Fatal(err)
	}
	aad := envelope.AAD("staging", "platform", "auth.jwt.private_key", 3)
	blob, err := envelope.Seal(oldKEK, []byte("rotate-me"), aad)
	if err != nil {
		t.Fatalf("Seal old: %v", err)
	}
	if blob.KEKKeyID != "local/1" {
		t.Fatalf("kek_key_id = %q, want local/1", blob.KEKKeyID)
	}
	// 双钥窗口：旧钥仍可 unwrap。
	plain, err := envelope.Open(oldKEK, blob, aad)
	if err != nil {
		t.Fatalf("Open old: %v", err)
	}
	// rewrap：明文用新 KEK 重新 seal。
	rewrapped, err := envelope.Seal(newKEK, plain, aad)
	if err != nil {
		t.Fatalf("Seal new: %v", err)
	}
	if rewrapped.KEKKeyID != "local/2" {
		t.Fatalf("rewrapped kek_key_id = %q, want local/2", rewrapped.KEKKeyID)
	}
	got, err := envelope.Open(newKEK, rewrapped, aad)
	if err != nil {
		t.Fatalf("Open new: %v", err)
	}
	if string(got) != "rotate-me" {
		t.Fatalf("plaintext after rewrap = %q", got)
	}
	// 旧钥下线：无法 unwrap 新密文。
	if _, err := envelope.Open(oldKEK, rewrapped, aad); err == nil {
		t.Fatal("old KEK must fail unwrap after rewrap")
	}
}
