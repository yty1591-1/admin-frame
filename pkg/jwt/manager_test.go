package jwt_test

import (
	"crypto/rand"
	"crypto/rsa"
	stderrors "errors"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/jwt"
)

func TestHMACSignParseRoundTrip(t *testing.T) {
	secret := bytes32("test-hmac-secret-key-32bytes!!")
	m, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "admin-api",
		Audience: []string{"admin"},
		Secret:   secret,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	tok, err := m.Sign(jwt.Claims{
		Subject: "sub-1",
		Extra:   map[string]any{"merchant_id": "m1", "roles": []string{"admin"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := m.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "sub-1" || got.Issuer != "admin-api" {
		t.Fatalf("got=%+v", got)
	}
	if got.GetString("merchant_id") != "m1" {
		t.Fatalf("merchant=%q", got.GetString("merchant_id"))
	}
	roles := got.GetStringSlice("roles")
	if len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("roles=%v", roles)
	}
}

func TestHMACRejectsWrongAudience(t *testing.T) {
	secret := bytes32("test-hmac-secret-key-32bytes!!")
	issuer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "admin-api", Audience: []string{"admin"}, Secret: secret, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := issuer.Sign(jwt.Claims{Subject: "u"})
	if err != nil {
		t.Fatal(err)
	}

	other, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "admin-api",
		Audience: []string{"biz"},
		Secret:   secret,
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = other.Parse(tok)
	if !stderrors.Is(err, jwt.ErrInvalidAudience) {
		t.Fatalf("err=%v", err)
	}
}

func TestHMACExpired(t *testing.T) {
	clock := jwt.NewManualClock(time.Unix(1_700_000_000, 0).UTC())
	secret := bytes32("test-hmac-secret-key-32bytes!!")
	m, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "c-api",
		Audience: []string{"c"},
		Secret:   secret,
		TTL:      time.Minute,
		Clock:    clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := m.Sign(jwt.Claims{Subject: "u"})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	_, err = m.Parse(tok)
	if !stderrors.Is(err, jwt.ErrExpired) {
		t.Fatalf("err=%v", err)
	}
}

func TestHMACKeyRotation(t *testing.T) {
	oldKey := bytes32("old-hmac-secret-key-32bytes!!!!")
	newKey := bytes32("new-hmac-secret-key-32bytes!!!!")
	oldMgr, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "biz-api", Audience: []string{"biz"}, Secret: oldKey, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := oldMgr.Sign(jwt.Claims{Subject: "staff"})
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "biz-api", Audience: []string{"biz"}, Secret: newKey, Secrets: [][]byte{oldKey}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := rotated.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "staff" {
		t.Fatalf("sub=%s", got.Subject)
	}
}

func TestRSASignParse(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m, err := jwt.NewRSA(jwt.RSAConfig{
		Issuer:     "admin-api",
		Audience:   []string{"admin"},
		PrivateKey: priv,
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := m.Sign(jwt.Claims{Subject: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "ops" {
		t.Fatalf("sub=%s", got.Subject)
	}
}

func TestRejectAlgNone(t *testing.T) {
	secret := bytes32("test-hmac-secret-key-32bytes!!")
	m, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "c-api", Audience: []string{"c"}, Secret: secret, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 手工构造 alg=none 的伪 Token（无签名）
	none := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJpc3MiOiJjLWFwaSIsImF1ZCI6ImMiLCJzdWIiOiJ1IiwiZXhwIjo5OTk5OTk5OTk5fQ."
	_, err = m.Parse(none)
	if !stderrors.Is(err, jwt.ErrInvalidToken) && !stderrors.Is(err, jwt.ErrUnsupportedAlgorithm) {
		t.Fatalf("err=%v", err)
	}
}

func TestExtraCannotOverrideRegistered(t *testing.T) {
	secret := bytes32("test-hmac-secret-key-32bytes!!")
	m, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "c-api", Audience: []string{"c"}, Secret: secret, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Sign(jwt.Claims{
		Subject: "u",
		Extra:   map[string]any{"iss": "evil"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewHMACRequiresStrongSecret(t *testing.T) {
	_, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer: "a", Audience: []string{"a"}, Secret: []byte("short"), TTL: time.Hour,
	})
	if !stderrors.Is(err, jwt.ErrMissingSecret) {
		t.Fatalf("err=%v", err)
	}
}

func bytes32(s string) []byte {
	b := make([]byte, 32)
	copy(b, s)
	return b
}
