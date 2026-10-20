package security_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/jwt"
)

// writePEM 把 PEM 块写入临时目录并返回路径。
func writePEM(t *testing.T, name string, blocks ...*pem.Block) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	var data []byte
	for _, b := range blocks {
		data = append(data, pem.EncodeToMemory(b)...)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// rsaPublicPEM 生成 PKIX 格式的 RSA 公钥 PEM 块。
func rsaPublicPEM(t *testing.T, key *rsa.PrivateKey) *pem.Block {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return &pem.Block{Type: "PUBLIC KEY", Bytes: der}
}

// TestNewJWTAuthRS256KeyFormats 覆盖 PKIX / PKCS#1 / 证书三种公钥来源。
func TestNewJWTAuthRS256KeyFormats(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := &pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)}
	cert := selfSignedCertPEM(t, key)

	cases := []struct {
		name string
		blk  *pem.Block
	}{
		{"pkix", rsaPublicPEM(t, key)},
		{"pkcs1", pkcs1},
		{"certificate", cert},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writePEM(t, "pub.pem", tc.blk)
			mw, err := security.NewJWTAuth(security.JWTConfig{
				Alg:           security.JWTAlgRS256,
				Issuer:        "issuer",
				Audience:      []string{"admin"},
				PublicKeyFile: path,
			}, auth.ChannelAdmin)
			if err != nil {
				t.Fatalf("NewJWTAuth: %v", err)
			}
			if code := serve(t, mw, "Bearer "+signRS256(t, key)); code != http.StatusOK {
				t.Fatalf("status=%d want 200", code)
			}
		})
	}
}

// TestNewJWTAuthRS256Rotation 验证轮换公钥文件中的旧密钥仍可校验。
func TestNewJWTAuthRS256Rotation(t *testing.T) {
	current, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	primary := writePEM(t, "current.pem", rsaPublicPEM(t, current))
	rotation := writePEM(t, "rotation.pem", rsaPublicPEM(t, current), rsaPublicPEM(t, rotated))

	mw, err := security.NewJWTAuth(security.JWTConfig{
		Alg:            security.JWTAlgRS256,
		Issuer:         "issuer",
		Audience:       []string{"admin"},
		PublicKeyFile:  primary,
		PublicKeysFile: rotation,
	}, auth.ChannelAdmin)
	if err != nil {
		t.Fatalf("NewJWTAuth: %v", err)
	}
	if code := serve(t, mw, "Bearer "+signRS256(t, rotated)); code != http.StatusOK {
		t.Fatalf("rotated key status=%d want 200", code)
	}
}

// TestNewJWTAuthRS256RejectsExpired 验证过期令牌被拒绝。
func TestNewJWTAuthRS256RejectsExpired(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := writePEM(t, "pub.pem", rsaPublicPEM(t, key))
	mw, err := security.NewJWTAuth(security.JWTConfig{
		Alg:           security.JWTAlgRS256,
		Issuer:        "issuer",
		Audience:      []string{"admin"},
		PublicKeyFile: path,
	}, auth.ChannelAdmin)
	if err != nil {
		t.Fatalf("NewJWTAuth: %v", err)
	}
	signer := newRSASigner(t, key)
	now := time.Now().UTC()
	token, err := signer.Sign(jwt.Claims{
		Subject:   "admin-1",
		IssuedAt:  now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if code := serve(t, mw, "Bearer "+token); code != http.StatusUnauthorized {
		t.Fatalf("expired token status=%d want 401", code)
	}
}

// TestNewJWTAuthRS256InvalidKeyFiles 覆盖公钥文件的各类非法输入。
func TestNewJWTAuthRS256InvalidKeyFiles(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path func(t *testing.T) string
	}{
		{"missing file", func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "nope.pem")
		}},
		{"no pem block", func(t *testing.T) string {
			return writePEM(t, "empty.pem")
		}},
		{"unsupported block", func(t *testing.T) string {
			return writePEM(t, "unsupported.pem", &pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("x")})
		}},
		{"corrupt pkix", func(t *testing.T) string {
			return writePEM(t, "corrupt.pem", &pem.Block{Type: "PUBLIC KEY", Bytes: []byte("not-der")})
		}},
		{"corrupt pkcs1", func(t *testing.T) string {
			return writePEM(t, "corrupt1.pem", &pem.Block{Type: "RSA PUBLIC KEY", Bytes: []byte("not-der")})
		}},
		{"corrupt certificate", func(t *testing.T) string {
			return writePEM(t, "corrupt-cert.pem", &pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-der")})
		}},
		{"non-rsa pkix", func(t *testing.T) string {
			return writePEM(t, "ec.pem", &pem.Block{Type: "PUBLIC KEY", Bytes: ecDER})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := security.NewJWTAuth(security.JWTConfig{
				Alg:           security.JWTAlgRS256,
				Issuer:        "issuer",
				Audience:      []string{"admin"},
				PublicKeyFile: tc.path(t),
			}, auth.ChannelAdmin)
			if err == nil {
				t.Fatal("NewJWTAuth accepted invalid key file")
			}
		})
	}
}

// TestNewJWTAuthRS256RejectsNonRSACertificate 覆盖证书公钥非 RSA 的分支。
func TestNewJWTAuthRS256RejectsNonRSACertificate(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ec"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &ecKey.PublicKey, ecKey)
	if err != nil {
		t.Fatal(err)
	}
	path := writePEM(t, "ec-cert.pem", &pem.Block{Type: "CERTIFICATE", Bytes: der})

	_, err = security.NewJWTAuth(security.JWTConfig{
		Alg:           security.JWTAlgRS256,
		Issuer:        "issuer",
		Audience:      []string{"admin"},
		PublicKeyFile: path,
	}, auth.ChannelAdmin)
	if err == nil {
		t.Fatal("NewJWTAuth accepted non-RSA certificate")
	}
}

// TestNewJWTAuthRS256RotationFileInvalid 覆盖轮换公钥文件读取失败。
func TestNewJWTAuthRS256RotationFileInvalid(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	primary := writePEM(t, "current.pem", rsaPublicPEM(t, key))
	_, err = security.NewJWTAuth(security.JWTConfig{
		Alg:            security.JWTAlgRS256,
		Issuer:         "issuer",
		Audience:       []string{"admin"},
		PublicKeyFile:  primary,
		PublicKeysFile: filepath.Join(t.TempDir(), "missing-rotation.pem"),
	}, auth.ChannelAdmin)
	if err == nil {
		t.Fatal("NewJWTAuth accepted missing rotation file")
	}
}

// TestNewJWTAuthRejectsInvalidConfig 验证启动期配置校验失败即返回错误。
func TestNewJWTAuthRejectsInvalidConfig(t *testing.T) {
	_, err := security.NewJWTAuth(security.JWTConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   "short",
	}, auth.ChannelAdmin)
	if err == nil {
		t.Fatal("NewJWTAuth accepted short secret")
	}
}

// TestNewJWTAuthRejectsShortRotationSecret 覆盖轮换密钥长度校验。
func TestNewJWTAuthRejectsShortRotationSecret(t *testing.T) {
	_, err := security.NewJWTAuth(security.JWTConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   "0123456789abcdef0123456789abcdef",
		Secrets:  []string{"short"},
	}, auth.ChannelAdmin)
	if err == nil {
		t.Fatal("NewJWTAuth accepted short rotation secret")
	}
}

// TestNewJWTAuthAcceptsRotationSecret 验证轮换密钥签发的令牌仍可校验。
func TestNewJWTAuthAcceptsRotationSecret(t *testing.T) {
	const current = "0123456789abcdef0123456789abcdef"
	const old = "fedcba9876543210fedcba9876543210"
	mw, err := security.NewJWTAuth(security.JWTConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   current,
		Secrets:  []string{old},
	}, auth.ChannelAdmin)
	if err != nil {
		t.Fatal(err)
	}
	oldSigner, err := jwt.NewHMAC(jwt.HMACConfig{
		Issuer:   "issuer",
		Audience: []string{"admin"},
		Secret:   []byte(old),
		TTL:      time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := oldSigner.Sign(jwt.Claims{Subject: "admin-1"})
	if err != nil {
		t.Fatal(err)
	}
	if code := serve(t, mw, "Bearer "+token); code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}
}

// signRS256 用给定私钥签发 admin 端令牌。
func signRS256(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	token, err := newRSASigner(t, key).Sign(jwt.Claims{Subject: "admin-1"})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// newRSASigner 构造与测试配置一致的 RS256 签发器。
func newRSASigner(t *testing.T, key *rsa.PrivateKey) *jwt.Manager {
	t.Helper()
	signer, err := jwt.NewRSA(jwt.RSAConfig{
		Issuer:     "issuer",
		Audience:   []string{"admin"},
		PrivateKey: key,
		TTL:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// selfSignedCertPEM 生成携带 RSA 公钥的自签名证书 PEM 块。
func selfSignedCertPEM(t *testing.T, key *rsa.PrivateKey) *pem.Block {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "rsa"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &pem.Block{Type: "CERTIFICATE", Bytes: der}
}
