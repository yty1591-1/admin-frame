package saml

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	testKeyOnce sync.Once
	testCertPEM string
	testKeyPEM  string
	testCertB64 string
)

// testSPKeys 生成一次 2048 位自签名证书，供本包用例复用。
func testSPKeys(t *testing.T) (certPEM, keyPEM, certB64 string) {
	t.Helper()
	testKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		template := x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "admin-frame-sp"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			panic(err)
		}
		testCertPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		testKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
		testCertB64 = base64.StdEncoding.EncodeToString(der)
	})
	return testCertPEM, testKeyPEM, testCertB64
}

// testIDPMetadata 返回包含签名证书与 Redirect SSO 的 IdP 元数据。
func testIDPMetadata(t *testing.T, entityID string) string {
	t.Helper()
	_, _, certB64 := testSPKeys(t)
	return `<?xml version="1.0" encoding="UTF-8"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="` + entityID + `">
  <IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <KeyDescriptor use="signing">
      <KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#">
        <X509Data><X509Certificate>` + certB64 + `</X509Certificate></X509Data>
      </KeyInfo>
    </KeyDescriptor>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.com/sso"/>
  </IDPSSODescriptor>
</EntityDescriptor>`
}

// testProvider 构造指向内联元数据的 Provider。
func testProvider(t *testing.T) *Provider {
	t.Helper()
	certPEM, keyPEM, _ := testSPKeys(t)
	provider, err := NewProvider(context.Background(), Config{
		Name:         "test",
		IDPEntityID:  "https://idp.example.com/metadata",
		MetadataXML:  testIDPMetadata(t, "https://idp.example.com/metadata"),
		Timeout:      2 * time.Second,
		MetadataTTL:  time.Hour,
		ClockSkew:    2 * time.Minute,
		AssertionTTL: 5 * time.Minute,
		SP: SPConfig{
			EntityID:       "https://sp.example.com/metadata",
			AcsURL:         "https://sp.example.com/c/v1/auth/saml/acs",
			CertificatePEM: certPEM,
			PrivateKeyPEM:  keyPEM,
		},
	})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return provider
}

// TestNewProviderRequiresMetadata 校验元数据缺失时失败关闭。
func TestNewProviderRequiresMetadata(t *testing.T) {
	t.Parallel()
	certPEM, keyPEM, _ := testSPKeys(t)
	_, err := NewProvider(context.Background(), Config{
		Name:        "test",
		IDPEntityID: "https://idp.example.com/metadata",
		Timeout:     time.Second,
		SP: SPConfig{
			EntityID:       "https://sp.example.com/metadata",
			AcsURL:         "https://sp.example.com/c/v1/auth/saml/acs",
			CertificatePEM: certPEM,
			PrivateKeyPEM:  keyPEM,
		},
	})
	if err == nil {
		t.Fatalf("expected metadata error")
	}
}

// TestNewProviderRejectsEntityIDMismatch 校验元数据 entityID 与配置不一致时失败。
func TestNewProviderRejectsEntityIDMismatch(t *testing.T) {
	t.Parallel()
	certPEM, keyPEM, _ := testSPKeys(t)
	_, err := NewProvider(context.Background(), Config{
		Name:        "test",
		IDPEntityID: "https://idp.example.com/other",
		MetadataXML: testIDPMetadata(t, "https://idp.example.com/metadata"),
		Timeout:     time.Second,
		SP: SPConfig{
			EntityID:       "https://sp.example.com/metadata",
			AcsURL:         "https://sp.example.com/c/v1/auth/saml/acs",
			CertificatePEM: certPEM,
			PrivateKeyPEM:  keyPEM,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "entity_id mismatch") {
		t.Fatalf("expected entity_id mismatch, got %v", err)
	}
}

// TestAuthorizationURLBindsRequestID 校验 SAMLRequest 与 RelayState 写入跳转地址。
func TestAuthorizationURLBindsRequestID(t *testing.T) {
	t.Parallel()
	provider := testProvider(t)
	raw, err := provider.AuthorizationURL("_request-1", "state-1")
	if err != nil {
		t.Fatalf("authorization url: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Query().Get("RelayState") != "state-1" {
		t.Fatalf("relay state missing: %s", raw)
	}
	if parsed.Query().Get("SAMLRequest") == "" {
		t.Fatalf("saml request missing: %s", raw)
	}
}

// TestParseResponseRejectsInvalidPayload 校验非法 Base64 与空断言统一失败。
func TestParseResponseRejectsInvalidPayload(t *testing.T) {
	t.Parallel()
	provider := testProvider(t)
	if _, err := provider.ParseResponse(context.Background(), "not-base64!", "_request-1"); err == nil {
		t.Fatalf("expected invalid response error")
	}
	if _, err := provider.ParseResponse(context.Background(), "", "_request-1"); err == nil {
		t.Fatalf("expected empty response error")
	}
}

// TestSPMetadataXMLPublishesACSAndCertificates 校验元数据包含 ACS 与轮换证书。
func TestSPMetadataXMLPublishesACSAndCertificates(t *testing.T) {
	t.Parallel()
	certPEM, keyPEM, _ := testSPKeys(t)
	spCfg := SPConfig{
		EntityID:        "https://sp.example.com/metadata",
		AcsURL:          "https://sp.example.com/c/v1/auth/saml/acs",
		CertificatePEM:  certPEM,
		PrivateKeyPEM:   keyPEM,
		CertificateNext: certPEM,
	}
	raw, err := SPMetadataXML(spCfg)
	if err != nil {
		t.Fatalf("sp metadata: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"https://sp.example.com/metadata",
		"https://sp.example.com/c/v1/auth/saml/acs",
		"AssertionConsumerService",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metadata missing %q:\n%s", want, text)
		}
	}
}

// TestParseKeyPairRejectsMismatch 校验证书与私钥不匹配时失败。
func TestParseKeyPairRejectsMismatch(t *testing.T) {
	t.Parallel()
	certPEM, _, _ := testSPKeys(t)
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	otherPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(otherKey)}))
	if _, err := ParseKeyPair(certPEM, otherPEM); err == nil {
		t.Fatalf("expected key mismatch error")
	}
}
