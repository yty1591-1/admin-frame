package jwt_test

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"

	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// hmacSecret 生成满足最小长度的确定性密钥。
func hmacSecret(fill byte) []byte {
	return []byte(strings.Repeat(string([]byte{fill}), 32))
}

// baseClaims 构造带统一时间的声明，避免测试依赖真实时钟。
func baseClaims(now time.Time) jwt.Claims {
	return jwt.Claims{
		Subject:   "acct-1",
		Audience:  []string{"admin"},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Minute),
		ID:        "jti-1",
		Extra:     map[string]any{"ver": int64(1), "sid": "sess-1"},
	}
}

func TestHMACKeyRingSignsWithActiveKID(t *testing.T) {
	now := time.Now().UTC()
	mgr, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		TTL:      time.Minute,
		KeyID:    "k2",
		Keys: []jwt.HMACKey{
			{KID: "k1", Secret: hmacSecret('a')},
			{KID: "k2", Secret: hmacSecret('b')},
		},
	})
	if err != nil {
		t.Fatalf("new hmac key ring: %v", err)
	}
	if mgr.KeyID() != "k2" {
		t.Fatalf("active kid = %q, want k2", mgr.KeyID())
	}

	token, err := mgr.Sign(baseClaims(now))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parsed, _, err := jwtv5.NewParser().ParseUnverified(token, jwtv5.MapClaims{})
	if err != nil {
		t.Fatalf("parse unverified: %v", err)
	}
	if kid, _ := parsed.Header["kid"].(string); kid != "k2" {
		t.Fatalf("kid header = %q, want k2", kid)
	}

	claims, err := mgr.Parse(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "acct-1" || claims.ID != "jti-1" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestHMACKeyRingRejectsUnknownAndMissingKID(t *testing.T) {
	now := time.Now().UTC()
	signer, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		TTL:      time.Minute,
		KeyID:    "k1",
		Keys:     []jwt.HMACKey{{KID: "k1", Secret: hmacSecret('a')}},
	})
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	token, err := signer.Sign(baseClaims(now))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// 校验端密钥环缺少 k1：必须按未知 kid 拒绝，而不是回退到其他密钥。
	verifier, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		TTL:      time.Minute,
		KeyID:    "k2",
		Keys:     []jwt.HMACKey{{KID: "k2", Secret: hmacSecret('b')}},
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if _, err := verifier.Parse(token); !errors.Is(err, jwt.ErrUnknownKeyID) {
		t.Fatalf("unknown kid error = %v, want ErrUnknownKeyID", err)
	}

	// 旧版单密钥签发的 Token 不带 kid；启用密钥环后必须拒绝。
	legacy, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		TTL:      time.Minute,
		Secret:   hmacSecret('a'),
	})
	if err != nil {
		t.Fatalf("new legacy signer: %v", err)
	}
	legacyToken, err := legacy.Sign(baseClaims(now))
	if err != nil {
		t.Fatalf("legacy sign: %v", err)
	}
	if _, err := signer.Parse(legacyToken); !errors.Is(err, jwt.ErrUnknownKeyID) {
		t.Fatalf("missing kid error = %v, want ErrUnknownKeyID", err)
	}
}

func TestRSAKeyRingRotatesAcrossKIDs(t *testing.T) {
	now := time.Now().UTC()
	oldKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate old key: %v", err)
	}
	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate new key: %v", err)
	}

	oldSigner, err := jwt.NewRSA(jwt.RSAConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		TTL:      time.Minute,
		KeyID:    "k-old",
		Keys:     []jwt.RSAKey{{KID: "k-old", PrivateKey: oldKey}},
	})
	if err != nil {
		t.Fatalf("old signer: %v", err)
	}
	oldToken, err := oldSigner.Sign(baseClaims(now))
	if err != nil {
		t.Fatalf("old sign: %v", err)
	}

	// 轮换期：新 kid 签发，旧公钥仍可校验存量 Token。
	rotated, err := jwt.NewRSA(jwt.RSAConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		TTL:      time.Minute,
		KeyID:    "k-new",
		Keys: []jwt.RSAKey{
			{KID: "k-old", PublicKey: &oldKey.PublicKey},
			{KID: "k-new", PrivateKey: newKey},
		},
	})
	if err != nil {
		t.Fatalf("rotated signer: %v", err)
	}
	if _, err := rotated.Parse(oldToken); err != nil {
		t.Fatalf("parse old token after rotation: %v", err)
	}

	newToken, err := rotated.Sign(baseClaims(now))
	if err != nil {
		t.Fatalf("new sign: %v", err)
	}
	parsed, _, err := jwtv5.NewParser().ParseUnverified(newToken, jwtv5.MapClaims{})
	if err != nil {
		t.Fatalf("parse unverified: %v", err)
	}
	if kid, _ := parsed.Header["kid"].(string); kid != "k-new" {
		t.Fatalf("kid header = %q, want k-new", kid)
	}

	oldOnly, err := jwt.NewRSA(jwt.RSAConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		TTL:      time.Minute,
		KeyID:    "k-old",
		Keys:     []jwt.RSAKey{{KID: "k-old", PublicKey: &oldKey.PublicKey}},
	})
	if err != nil {
		t.Fatalf("old-only verifier: %v", err)
	}
	if _, err := oldOnly.Parse(newToken); !errors.Is(err, jwt.ErrUnknownKeyID) {
		t.Fatalf("new kid on old verifier error = %v, want ErrUnknownKeyID", err)
	}
}
