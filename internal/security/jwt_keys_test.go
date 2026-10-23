package security_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// writeRSAKeyPair 生成测试用 RSA 密钥对并返回 PEM 文件路径。
func writeRSAKeyPair(t *testing.T) (publicPath, privatePath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	dir := t.TempDir()
	publicPath = filepath.Join(dir, "jwt.pub.pem")
	privatePath = filepath.Join(dir, "jwt.key.pem")

	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicDER,
	}), 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return publicPath, privatePath
}

// keyRingConfig 构造 RS256 kid 密钥环配置。
func keyRingConfig(publicPath, privatePath string) security.JWTConfig {
	return security.JWTConfig{
		Alg:       security.JWTAlgRS256,
		Issuer:    "https://identity.example.com",
		Audience:  []string{"admin"},
		ActiveKID: "2026-10",
		Keys: []security.JWTKeyConfig{{
			KID:            "2026-10",
			PublicKeyFile:  publicPath,
			PrivateKeyFile: privatePath,
		}},
	}
}

func TestJWTConfigKeyRingValidation(t *testing.T) {
	publicPath, privatePath := writeRSAKeyPair(t)

	cases := []struct {
		name   string
		mutate func(*security.JWTConfig)
	}{
		{
			name: "keys with legacy secret",
			mutate: func(c *security.JWTConfig) {
				c.Secret = "01234567890123456789012345678901"
			},
		},
		{
			name: "missing active kid",
			mutate: func(c *security.JWTConfig) {
				c.ActiveKID = ""
			},
		},
		{
			name: "active kid not in ring",
			mutate: func(c *security.JWTConfig) {
				c.ActiveKID = "2026-11"
			},
		},
		{
			name: "duplicated kid",
			mutate: func(c *security.JWTConfig) {
				c.Keys = append(c.Keys, security.JWTKeyConfig{
					KID:           "2026-10",
					PublicKeyFile: publicPath,
				})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := keyRingConfig(publicPath, privatePath)
			tc.mutate(&cfg)
			if err := cfg.Validate("admin"); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	if err := keyRingConfig(publicPath, privatePath).Validate("admin"); err != nil {
		t.Fatalf("valid key ring rejected: %v", err)
	}
}

func TestJWTKeyRingSignAndPrincipalMapping(t *testing.T) {
	publicPath, privatePath := writeRSAKeyPair(t)
	cfg := keyRingConfig(publicPath, privatePath)

	signer, err := cfg.SigningManager("admin")
	if err != nil {
		t.Fatalf("signing manager: %v", err)
	}
	now := time.Now().UTC()
	token, err := signer.Sign(jwt.Claims{
		Subject:   "acct-1",
		Audience:  []string{"admin"},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Minute),
		ID:        "jti-1",
		Extra: map[string]any{
			"ver":     int64(3),
			"sid":     "sess-1",
			"channel": "admin",
		},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	mw, err := security.NewJWTAuth(cfg, auth.ChannelAdmin)
	if err != nil {
		t.Fatalf("new jwt auth: %v", err)
	}

	var captured auth.Principal
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.FromContext(r.Context())
		if !ok {
			t.Error("principal missing from context")
			return
		}
		captured = p
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/iam/me/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if captured.SubjectID != "acct-1" || captured.SessionID != "sess-1" ||
		captured.TokenID != "jti-1" || captured.TokenVersion != 3 {
		t.Fatalf("unexpected principal: %+v", captured)
	}
	if captured.Channel != auth.ChannelAdmin {
		t.Fatalf("channel = %q, want admin", captured.Channel)
	}
	if captured.TokenExpiresAt.IsZero() {
		t.Fatal("token expiry must be mapped into principal")
	}
}

func TestJWTKeyRingRejectsUnknownKID(t *testing.T) {
	publicPath, privatePath := writeRSAKeyPair(t)
	issuerCfg := keyRingConfig(publicPath, privatePath)
	signer, err := issuerCfg.SigningManager("admin")
	if err != nil {
		t.Fatalf("signing manager: %v", err)
	}
	now := time.Now().UTC()
	token, err := signer.Sign(jwt.Claims{
		Subject:   "acct-1",
		Audience:  []string{"admin"},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Minute),
		ID:        "jti-1",
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// 校验端只认识另一个 kid：必须 401，而不是尝试其他公钥。
	verifierCfg := keyRingConfig(publicPath, privatePath)
	verifierCfg.ActiveKID = "2026-11"
	verifierCfg.Keys[0].KID = "2026-11"
	mw, err := security.NewJWTAuth(verifierCfg, auth.ChannelAdmin)
	if err != nil {
		t.Fatalf("new jwt auth: %v", err)
	}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/iam/me/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}
