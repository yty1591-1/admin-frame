package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// writeSecurityTestRSA 生成 RSA 密钥对，返回 PKIX 公钥与 PKCS#8 私钥文件路径。
func writeSecurityTestRSA(t *testing.T) (key *rsa.PrivateKey, publicPath, privatePath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	dir := t.TempDir()
	publicPath = filepath.Join(dir, "jwt.pub.pem")
	privatePath = filepath.Join(dir, "jwt.key.pem")
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicDER,
	}), 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateDER,
	}), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return key, publicPath, privatePath
}

// TestClaimInt64Formats 覆盖 Extra 声明可能出现的全部数值类型。
func TestClaimInt64Formats(t *testing.T) {
	if got := claimInt64(nil, "ver"); got != 0 {
		t.Fatalf("nil extra = %d, want 0", got)
	}
	cases := []struct {
		name  string
		value any
		want  int64
	}{
		{"int64", int64(1), 1},
		{"int", 2, 2},
		{"float64", float64(3), 3},
		{"json number", json.Number("4"), 4},
		{"invalid json number", json.Number("x"), 0},
		{"unsupported", "5", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimInt64(map[string]any{"ver": tc.value}, "ver"); got != tc.want {
				t.Fatalf("claimInt64(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
	if got := claimInt64(map[string]any{}, "ver"); got != 0 {
		t.Fatalf("missing claim = %d, want 0", got)
	}
}

// TestParseRSAPublicKeyFormats 覆盖 PKIX、PKCS#1、证书与非法 PEM 块。
func TestParseRSAPublicKeyFormats(t *testing.T) {
	key, _, _ := writeSecurityTestRSA(t)

	if _, err := parseRSAPublicKey(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey),
	}); err != nil {
		t.Fatalf("parse pkcs1 public key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "security-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	if _, err := parseRSAPublicKey(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	edPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	edDER, err := x509.MarshalPKIXPublicKey(edPublic)
	if err != nil {
		t.Fatalf("marshal ed25519 key: %v", err)
	}

	errCases := []struct {
		name  string
		block *pem.Block
	}{
		{"invalid pkix", &pem.Block{Type: "PUBLIC KEY", Bytes: []byte("bad")}},
		{"non-rsa pkix", &pem.Block{Type: "PUBLIC KEY", Bytes: edDER}},
		{"invalid pkcs1", &pem.Block{Type: "RSA PUBLIC KEY", Bytes: []byte("bad")}},
		{"invalid certificate", &pem.Block{Type: "CERTIFICATE", Bytes: []byte("bad")}},
		{"unsupported block", &pem.Block{Type: "EC PUBLIC KEY", Bytes: []byte("bad")}},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseRSAPublicKey(tc.block); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

// TestLoadPrivateKeyFormats 覆盖 PKCS#1、PKCS#8 与非法文件分支。
func TestLoadPrivateKeyFormats(t *testing.T) {
	key, _, pkcs8Path := writeSecurityTestRSA(t)

	if _, err := loadPrivateKey(pkcs8Path); err != nil {
		t.Fatalf("load pkcs8 private key: %v", err)
	}

	pkcs1Path := filepath.Join(t.TempDir(), "pkcs1.key.pem")
	if err := os.WriteFile(pkcs1Path, pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), 0o600); err != nil {
		t.Fatalf("write pkcs1 private key: %v", err)
	}
	if _, err := loadPrivateKey(pkcs1Path); err != nil {
		t.Fatalf("load pkcs1 private key: %v", err)
	}

	dir := t.TempDir()
	notPEM := filepath.Join(dir, "not.pem")
	if err := os.WriteFile(notPEM, []byte("not a pem block"), 0o600); err != nil {
		t.Fatalf("write not-pem file: %v", err)
	}
	unsupported := filepath.Join(dir, "ec.pem")
	if err := os.WriteFile(unsupported, pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: []byte("bad"),
	}), 0o600); err != nil {
		t.Fatalf("write unsupported key: %v", err)
	}
	_, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	edDER, err := x509.MarshalPKCS8PrivateKey(edPrivate)
	if err != nil {
		t.Fatalf("marshal ed25519 private key: %v", err)
	}
	nonRSA := filepath.Join(dir, "ed25519.pem")
	if err := os.WriteFile(nonRSA, pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: edDER,
	}), 0o600); err != nil {
		t.Fatalf("write non-rsa private key: %v", err)
	}

	for name, path := range map[string]string{
		"missing file":      filepath.Join(dir, "missing.pem"),
		"no pem block":      notPEM,
		"unsupported block": unsupported,
		"non-rsa pkcs8 key": nonRSA,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadPrivateKey(path); err == nil {
				t.Fatal("expected load error")
			}
		})
	}
}

// TestSigningManagerHS256 覆盖 HMAC 签发路径与轮换密钥转换。
func TestSigningManagerHS256(t *testing.T) {
	cfg := JWTConfig{
		Alg:      JWTAlgHS256,
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   strings.Repeat("s", minHMACSecretBytes),
		Secrets:  []string{strings.Repeat("o", minHMACSecretBytes)},
	}
	manager, err := cfg.SigningManager("admin")
	if err != nil {
		t.Fatalf("signing manager: %v", err)
	}
	if _, err := manager.Sign(jwt.Claims{Subject: "admin-1"}); err != nil {
		t.Fatalf("sign: %v", err)
	}
}

// TestSigningManagerRS256RequiresPrivateKey 覆盖签发进程缺少私钥的错误分支。
func TestSigningManagerRS256RequiresPrivateKey(t *testing.T) {
	_, publicPath, _ := writeSecurityTestRSA(t)
	cfg := JWTConfig{
		Alg:       JWTAlgRS256,
		Issuer:    "issuer",
		Audience:  []string{"admin"},
		ActiveKID: "kid-1",
		Keys: []JWTKeyConfig{{
			KID:           "kid-1",
			PublicKeyFile: publicPath,
		}},
	}
	if _, err := cfg.SigningManager("admin"); err == nil {
		t.Fatal("expected error when active key has no private key")
	}

	legacy := JWTConfig{
		Alg:           JWTAlgRS256,
		Issuer:        "issuer",
		Audience:      []string{"admin"},
		PublicKeyFile: publicPath,
	}
	if _, err := legacy.SigningManager("admin"); err == nil {
		t.Fatal("expected error when auth.jwt.keys is missing")
	}
}

// TestNewJWTAuthKeyRingErrors 覆盖密钥环文件读取与空公钥分支。
func TestNewJWTAuthKeyRingErrors(t *testing.T) {
	_, publicPath, _ := writeSecurityTestRSA(t)
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "not.pem")
	if err := os.WriteFile(notPEM, []byte("not a pem block"), 0o600); err != nil {
		t.Fatalf("write not-pem file: %v", err)
	}

	cases := []struct {
		name string
		key  JWTKeyConfig
	}{
		{"missing public key", JWTKeyConfig{KID: "kid-1", PublicKeyFile: filepath.Join(dir, "missing.pem")}},
		{"empty public key file", JWTKeyConfig{KID: "kid-1", PublicKeyFile: notPEM}},
		{"missing private key", JWTKeyConfig{KID: "kid-1", PublicKeyFile: publicPath, PrivateKeyFile: filepath.Join(dir, "missing.pem")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := JWTConfig{
				Alg:       JWTAlgRS256,
				Issuer:    "issuer",
				Audience:  []string{"admin"},
				ActiveKID: "kid-1",
				Keys:      []JWTKeyConfig{tc.key},
			}
			if _, err := NewJWTAuth(cfg, auth.ChannelAdmin); err == nil {
				t.Fatal("expected jwt auth construction error")
			}
		})
	}
}

// stubRevocationChecker 是 RevocationChecker 的测试实现。
type stubRevocationChecker struct{}

// Check 实现 RevocationChecker。
func (stubRevocationChecker) Check(context.Context, jwt.Claims) error { return nil }

// TestWithRevocationChecker 覆盖 nil 与非 nil 检查器注入。
func TestWithRevocationChecker(t *testing.T) {
	options := jwtAuthOptions{}
	WithRevocationChecker(nil)(&options)
	if options.checker != nil {
		t.Fatal("nil checker must not be installed")
	}
	WithRevocationChecker(stubRevocationChecker{})(&options)
	if options.checker == nil {
		t.Fatal("checker must be installed")
	}
}

// TestNopObservers 覆盖默认观测实现的空操作。
func TestNopObservers(_ *testing.T) {
	ctx := context.Background()
	NopShadowObserver{}.OnMismatch(ctx, Mismatch{Channel: "admin"})
	NopShadowObserver{}.OnLoadError(ctx, "admin", nil)
	NopDecisionObserver{}.OnDecision(ctx, "admin", DecisionAllow)
}

// TestPrincipalFromClaimsRejectsEmptySubject 覆盖 sub 缺失时的拒绝分支。
func TestPrincipalFromClaimsRejectsEmptySubject(t *testing.T) {
	if _, err := principalFromClaims(jwt.Claims{}, auth.ChannelAdmin); err == nil {
		t.Fatal("expected error for empty subject")
	}
}

// TestJWTConfigValidateRotatedSecrets 覆盖轮换密钥长度校验与负 leeway。
func TestJWTConfigValidateRotatedSecrets(t *testing.T) {
	cfg := JWTConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   strings.Repeat("s", minHMACSecretBytes),
	}
	if err := cfg.Validate("admin"); err != nil {
		t.Fatalf("valid rotated config rejected: %v", err)
	}

	cfg.Secrets = []string{"short"}
	if err := cfg.Validate("admin"); err == nil {
		t.Fatal("expected error for short rotated secret")
	}

	cfg.Secrets = nil
	cfg.Leeway = -time.Second
	if err := cfg.Validate("admin"); err == nil {
		t.Fatal("expected error for negative leeway")
	}
}
