package crypto_test

import (
	"bytes"
	stderrors "errors"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/crypto"
)

func TestAESGCMRoundTrip(t *testing.T) {
	key := crypto.MustRandomBytes(crypto.AES256KeySize)
	defer crypto.ClearBytes(key)

	aead, err := crypto.NewAES256GCM(key)
	if err != nil {
		t.Fatal(err)
	}
	defer aead.Close()

	pt := []byte("secret-payload")
	aad := []byte("tenant:m1")
	blob, err := aead.Seal(pt, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := aead.Open(blob, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("got=%q", got)
	}

	if _, err := aead.Open(blob, []byte("other")); !stderrors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("aad mismatch err=%v", err)
	}
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := aead.Open(tampered, aad); !stderrors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("tamper err=%v", err)
	}
}

func TestAESGCMRejectsBadKeySize(t *testing.T) {
	if _, err := crypto.NewAES256GCM([]byte("short")); !stderrors.Is(err, crypto.ErrInvalidKey) {
		t.Fatalf("err=%v", err)
	}
}

func TestHMACVerify(t *testing.T) {
	key := crypto.MustRandomBytes(32)
	msg := []byte("hello")
	mac, err := crypto.HMACSHA256(key, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := crypto.HMACSHA256Verify(key, msg, mac); err != nil {
		t.Fatal(err)
	}
	if err := crypto.HMACSHA256Verify(key, []byte("other"), mac); !stderrors.Is(err, crypto.ErrInvalidMAC) {
		t.Fatalf("err=%v", err)
	}
}

func TestHashAndEqual(t *testing.T) {
	a := crypto.SHA256([]byte("x"))
	b := crypto.SHA256([]byte("x"))
	if !crypto.Equal(a, b) {
		t.Fatal("equal")
	}
	if crypto.SHA256Hex([]byte("x")) == "" {
		t.Fatal("hex empty")
	}
	if len(crypto.SHA512([]byte("x"))) != 64 {
		t.Fatal("sha512 size")
	}
}

func TestWrapUnwrapKey(t *testing.T) {
	kek := crypto.MustRandomBytes(crypto.AES256KeySize)
	dek := crypto.MustRandomBytes(crypto.AES256KeySize)
	defer crypto.ClearBytes(kek)
	defer crypto.ClearBytes(dek)

	wrapped, err := crypto.WrapKey(kek, dek)
	if err != nil {
		t.Fatal(err)
	}
	got, err := crypto.UnwrapKey(kek, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	defer crypto.ClearBytes(got)
	if !crypto.Equal(got, dek) {
		t.Fatal("dek mismatch")
	}

	badKEK := crypto.MustRandomBytes(crypto.AES256KeySize)
	defer crypto.ClearBytes(badKEK)
	if _, err := crypto.UnwrapKey(badKEK, wrapped); !stderrors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("err=%v", err)
	}
}

func TestEd25519SignVerify(t *testing.T) {
	pub, priv, err := crypto.GenerateEd25519()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("payload")
	sig, err := crypto.SignEd25519(priv, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := crypto.VerifyEd25519(pub, msg, sig); err != nil {
		t.Fatal(err)
	}
	if err := crypto.VerifyEd25519(pub, []byte("nope"), sig); !stderrors.Is(err, crypto.ErrInvalidMAC) {
		t.Fatalf("err=%v", err)
	}
}

func TestRandomAndClear(t *testing.T) {
	b, err := crypto.RandomBytes(16)
	if err != nil || len(b) != 16 {
		t.Fatalf("b=%v err=%v", b, err)
	}
	crypto.ClearBytes(b)
	for _, x := range b {
		if x != 0 {
			t.Fatal("not cleared")
		}
	}
}
