package secretruntime

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadServerTLSFromEnvRequiresMTLSInProdLike(t *testing.T) {
	t.Setenv("CONFIG_API_TLS_CERT", "")
	t.Setenv("CONFIG_API_TLS_KEY", "")
	t.Setenv("CONFIG_API_TLS_CLIENT_CA", "")
	t.Setenv("CONFIG_API_MTLS", "off")

	_, err := LoadServerTLSFromEnvFor("prod")
	if err == nil {
		t.Fatal("prod + mTLS=off must fail")
	}
	_, err = LoadServerTLSFromEnvFor("staging")
	if err == nil {
		t.Fatal("staging + mTLS=off must fail")
	}
	_, err = LoadServerTLSFromEnvFor("local")
	if err != nil {
		t.Fatalf("local plaintext ok: %v", err)
	}
}

func TestLoadServerTLSFromEnvRequireNeedsCA(t *testing.T) {
	t.Setenv("CONFIG_API_TLS_CERT", "server.crt")
	t.Setenv("CONFIG_API_TLS_KEY", "server.key")
	t.Setenv("CONFIG_API_MTLS", "require")
	t.Setenv("CONFIG_API_TLS_CLIENT_CA", "")
	_, err := LoadServerTLSFromEnvFor("prod")
	if err == nil {
		t.Fatal("require without CA must fail")
	}
}

func TestLoadClientHTTPFromEnvProdRequiresCerts(t *testing.T) {
	t.Setenv("CONFIG_API_MTLS", "require")
	t.Setenv("CONFIG_API_CLIENT_CERT", "")
	t.Setenv("CONFIG_API_CLIENT_KEY", "")
	t.Setenv("CONFIG_API_TLS_CA", "")
	if _, err := LoadClientHTTPFromEnvFor("prod"); err == nil {
		t.Fatal("prod mTLS require without client cert must fail")
	}
	if _, err := LoadClientHTTPFromEnvFor("dev"); err != nil {
		t.Fatalf("dev plaintext client ok: %v", err)
	}
}

func TestLoadClientHTTPFromEnvWithFiles(t *testing.T) {
	dir := writeMTLSFixture(t)
	t.Setenv("CONFIG_API_MTLS", "require")
	t.Setenv("CONFIG_API_CLIENT_CERT", filepath.Join(dir, "client.crt"))
	t.Setenv("CONFIG_API_CLIENT_KEY", filepath.Join(dir, "client.key"))
	t.Setenv("CONFIG_API_TLS_CA", filepath.Join(dir, "ca.crt"))
	client, err := LoadClientHTTPFromEnvFor("prod")
	if err != nil {
		t.Fatalf("LoadClientHTTPFromEnvFor: %v", err)
	}
	if client == nil || client.Transport == nil {
		t.Fatal("expected TLS transport")
	}
}

func writeMTLSFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "prod-rehearsal-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}
	writePEM(t, filepath.Join(dir, "ca.crt"), "CERTIFICATE", caDER)
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	clientTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "prod-rehearsal-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	writePEM(t, filepath.Join(dir, "client.crt"), "CERTIFICATE", clientDER)
	keyBytes, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	writePEM(t, filepath.Join(dir, "client.key"), "EC PRIVATE KEY", keyBytes)
	return dir
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	f, err := os.Create(path) //nolint:gosec // 测试临时路径。
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: der}); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}
