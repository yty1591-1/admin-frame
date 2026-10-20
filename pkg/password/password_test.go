package password_test

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/admin-frame/admin-frame/pkg/password"
)

func TestHashVerifyArgon2idDefault(t *testing.T) {
	h := password.Must(password.Config{})
	if h.Algorithm() != password.AlgorithmArgon2id {
		t.Fatalf("algo=%s", h.Algorithm())
	}
	encoded, err := h.Hash([]byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$") {
		t.Fatalf("encoded=%s", encoded)
	}
	if err := h.Verify(encoded, []byte("correct horse battery staple")); err != nil {
		t.Fatal(err)
	}
	if err := h.Verify(encoded, []byte("wrong")); !errors.Is(err, password.ErrMismatch) {
		t.Fatalf("err=%v", err)
	}
	if h.NeedsRehash(encoded) {
		t.Fatal("fresh hash should not need rehash")
	}
}

func TestHashVerifyBcrypt(t *testing.T) {
	h := password.Must(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: bcrypt.MinCost}, // 测试加速
	})
	encoded, err := h.Hash([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := password.Verify(encoded, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := password.Verify(encoded, []byte("nope")); !errors.Is(err, password.ErrMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestNeedsRehashOnAlgorithmChange(t *testing.T) {
	bc := password.Must(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: bcrypt.MinCost},
	})
	encoded, err := bc.Hash([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	argon := password.Must(password.Config{})
	if !argon.NeedsRehash(encoded) {
		t.Fatal("bcrypt hash should need rehash under argon2id hasher")
	}
}

func TestNeedsRehashOnBcryptCost(t *testing.T) {
	low := password.Must(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: bcrypt.MinCost},
	})
	encoded, err := low.Hash([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	high := password.Must(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: bcrypt.MinCost + 1},
	})
	if !high.NeedsRehash(encoded) {
		t.Fatal("expected rehash on higher cost")
	}
}

func TestEmptyAndTooLong(t *testing.T) {
	h := password.Must(password.Config{})
	if _, err := h.Hash(nil); !errors.Is(err, password.ErrEmptyPassword) {
		t.Fatalf("err=%v", err)
	}
	long := make([]byte, password.MaxPasswordBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := h.Hash(long); !errors.Is(err, password.ErrPasswordTooLong) {
		t.Fatalf("err=%v", err)
	}

	bc := password.Must(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: bcrypt.MinCost},
	})
	tooLongForBcrypt := make([]byte, password.BcryptMaxBytes+1)
	for i := range tooLongForBcrypt {
		tooLongForBcrypt[i] = 'b'
	}
	if _, err := bc.Hash(tooLongForBcrypt); !errors.Is(err, password.ErrPasswordTooLong) {
		t.Fatalf("err=%v", err)
	}
}

func TestDetectAlgorithm(t *testing.T) {
	algo, err := password.DetectAlgorithm("$argon2id$v=19$m=1,t=1,p=1$aa$bb")
	if err != nil || algo != password.AlgorithmArgon2id {
		t.Fatalf("algo=%s err=%v", algo, err)
	}
	algo, err = password.DetectAlgorithm("$2a$10$abcdefghijklmnopqrstuu")
	if err != nil || algo != password.AlgorithmBcrypt {
		t.Fatalf("algo=%s err=%v", algo, err)
	}
	if _, err := password.DetectAlgorithm("plaintext"); !errors.Is(err, password.ErrUnsupportedAlgorithm) {
		t.Fatalf("err=%v", err)
	}
}

func TestInvalidHash(t *testing.T) {
	if err := password.Verify("$argon2id$bad", []byte("x")); !errors.Is(err, password.ErrInvalidHash) {
		t.Fatalf("err=%v", err)
	}
	if err := password.Verify("", []byte("x")); !errors.Is(err, password.ErrInvalidHash) {
		t.Fatalf("err=%v", err)
	}
}

func TestPackageHashHelper(t *testing.T) {
	enc, err := password.Hash([]byte("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := password.Verify(enc, []byte("demo")); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidBcryptCost(t *testing.T) {
	_, err := password.New(password.Config{
		Algorithm: password.AlgorithmBcrypt,
		Bcrypt:    password.BcryptParams{Cost: 99},
	})
	if !errors.Is(err, password.ErrInvalidConfig) {
		t.Fatalf("err=%v", err)
	}
}
