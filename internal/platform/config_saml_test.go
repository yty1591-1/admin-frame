package platform_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
)

var (
	samlKeyOnce sync.Once
	samlCertPEM string
	samlKeyPEM  string
)

// samlTestKeys 生成一次 2048 位自签名证书与私钥（仅测试用）。
func samlTestKeys(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	samlKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		template := x509.Certificate{
			SerialNumber:          big.NewInt(2),
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
		samlCertPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		samlKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	})
	return samlCertPEM, samlKeyPEM
}

// enableSAML 用测试密钥与内联 IdP 元数据填充最小 SAML 配置。
func enableSAML(t *testing.T, cfg *platform.Config) {
	t.Helper()
	certPEM, keyPEM := samlTestKeys(t)
	cfg.Identity.SAML = platform.IdentitySAMLConfig{
		Enabled:          true,
		EntityID:         "https://c.example.com/saml/metadata",
		ACSURL:           "https://c.example.com/c/v1/auth/saml/acs",
		LoginRedirectURL: "https://c.example.com/login",
		CertificatePEM:   certPEM,
		PrivateKey:       keyPEM,
		RateLimit:        platform.IdentityLoginRateLimitConfig{RPS: 1, Burst: 5, FailureMode: platform.RateLimitFailureClosed},
		Providers: []platform.IdentitySAMLProviderConfig{
			{
				Name:        "okta",
				IDPEntityID: "https://idp.example.com/metadata",
				MetadataURL: "https://idp.example.com/metadata",
			},
		},
	}
}

// TestValidateSAMLRules 校验 SAML 配置边界。
func TestValidateSAMLRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(cfg *platform.Config)
		want   string
	}{
		{
			name: "disabled with providers",
			mutate: func(cfg *platform.Config) {
				cfg.Identity.SAML.Providers = []platform.IdentitySAMLProviderConfig{{Name: "okta"}}
			},
			want: "requires identity.saml.enabled=true",
		},
		{
			name: "saml on biz channel",
			mutate: func(cfg *platform.Config) {
				cfg.Channel = "biz"
				cfg.Auth.ExpectedChannel = "biz"
				enableSAML(t, cfg)
			},
			want: "identity.saml is only supported on channel=c",
		},
		{
			name: "missing entity id",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.EntityID = ""
			},
			want: "identity.saml.entity_id is required",
		},
		{
			name: "acs path mismatch",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.ACSURL = "https://c.example.com/c/v1/auth/saml/other"
			},
			want: "identity.saml.acs_url path must be /c/v1/auth/saml/acs",
		},
		{
			name: "next cert without key",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.CertificateNextPEM = cfg.Identity.SAML.CertificatePEM
			},
			want: "must be set together",
		},
		{
			name: "request ttl too long",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.RequestTTL = 20 * time.Minute
			},
			want: "identity.saml.request_ttl must be in (0, 15m]",
		},
		{
			name: "ticket ttl too short",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.TicketTTL = 10 * time.Second
			},
			want: "identity.saml.ticket_ttl must be in [30s, 5m]",
		},
		{
			name: "replay ttl too short",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.ReplayTTL = time.Minute
			},
			want: "identity.saml.replay_ttl must be >= assertion_ttl + clock_skew",
		},
		{
			name: "provider without metadata",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.Providers[0].MetadataURL = ""
			},
			want: "requires one of metadata_url or metadata_xml",
		},
		{
			name: "provider with both metadata sources",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.Providers[0].MetadataXML = "<metadata/>"
			},
			want: "must not set both metadata_url and metadata_xml",
		},
		{
			name: "duplicate provider name",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.Providers = append(cfg.Identity.SAML.Providers, cfg.Identity.SAML.Providers[0])
			},
			want: `identity.saml.providers[1].name "okta" is duplicated`,
		},
		{
			name: "no providers when enabled",
			mutate: func(cfg *platform.Config) {
				enableSAML(t, cfg)
				cfg.Identity.SAML.Providers = nil
			},
			want: "identity.saml.providers is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cBaseConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

// TestValidateSAMLProdSatisfiesLoginMethods 校验仅启用 SAML 时生产的 C 端登录方式检查通过。
func TestValidateSAMLProdSatisfiesLoginMethods(t *testing.T) {
	cfg := cProdConfig()
	cfg.Identity.SMS.Enabled = false
	enableSAML(t, cfg)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid prod saml config, got %v", err)
	}
}

// TestValidateSAMLFailOpenRejectedInProd 校验生产不接受 fail_open。
func TestValidateSAMLFailOpenRejectedInProd(t *testing.T) {
	cfg := cProdConfig()
	enableSAML(t, cfg)
	cfg.Identity.SAML.RateLimit.FailureMode = platform.RateLimitFailureOpen
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "identity.saml.rate_limit.failure_mode must be fail_closed") {
		t.Fatalf("err=%v", err)
	}
}
