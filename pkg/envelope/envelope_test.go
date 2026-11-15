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
