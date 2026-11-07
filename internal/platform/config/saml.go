package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// parseSAMLKeyPair 解析并校验 SP 证书 / 私钥：配对、RSA ≥ 2048、证书未过期。
func parseSAMLKeyPair(certPEM, keyPEM string) (*x509.Certificate, *rsa.PrivateKey, error) {
	if strings.TrimSpace(certPEM) == "" || strings.TrimSpace(keyPEM) == "" {
		return nil, nil, errors.New("certificate and private key are required")
	}
	certBlock, _ := pem.Decode([]byte(certPEM))
	if certBlock == nil {
		return nil, nil, errors.New("certificate must be PEM encoded")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse certificate: %w", err)
	}
	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return nil, nil, errors.New("private key must be PEM encoded")
	}
	var key *rsa.PrivateKey
	if parsed, parseErr := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); parseErr == nil {
		key = parsed
	} else {
		parsedPKCS8, parseErr8 := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if parseErr8 != nil {
			return nil, nil, errors.New("private key must be PKCS#1 or PKCS#8 RSA")
		}
		rsaKey, ok := parsedPKCS8.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, errors.New("private key must be RSA")
		}
		key = rsaKey
	}
	if key.N.BitLen() < 2048 {
		return nil, nil, errors.New("private key must be RSA >= 2048 bits")
	}
	public, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || public.N.Cmp(key.N) != 0 || public.E != key.E {
		return nil, nil, errors.New("certificate does not match private key")
	}
	if time.Now().After(cert.NotAfter) {
		return nil, nil, fmt.Errorf("certificate expired at %s", cert.NotAfter.UTC().Format(time.RFC3339))
	}
	return cert, key, nil
}

// isOAuthProviderName 校验 provider 名：小写字母开头，允许 [a-z0-9_-]，长度 2-32。
func isOAuthProviderName(name string) bool {
	if len(name) < 2 || len(name) > 32 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		ch := name[i]
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

// validateAbsoluteURL 校验绝对 URL；requireHTTPS 为 true 时只接受 https。
func validateAbsoluteURL(raw string, requireHTTPS bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if requireHTTPS {
			return errors.New("must use https in prod/staging")
		}
		return nil
	default:
		return errors.New("scheme must be http or https")
	}
}
