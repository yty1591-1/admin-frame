// Package saml 解析 SP 密钥与 IdP 签名证书的 PEM。
package saml

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// KeyPair 是 SP 的证书与私钥。
type KeyPair struct {
	Certificate *x509.Certificate
	Key         *rsa.PrivateKey
}

// ParseKeyPair 解析并校验 SP 证书 / 私钥配对关系。
func ParseKeyPair(certPEM, keyPEM string) (KeyPair, error) {
	certs, err := ParseCertificates(certPEM)
	if err != nil {
		return KeyPair{}, err
	}
	if len(certs) == 0 {
		return KeyPair{}, errors.New("saml: certificate is required")
	}
	key, err := parsePrivateKey(keyPEM)
	if err != nil {
		return KeyPair{}, err
	}
	leaf := certs[0]
	public, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return KeyPair{}, errors.New("saml: certificate public key must be RSA")
	}
	if public.E != key.E || public.N.Cmp(key.N) != 0 {
		return KeyPair{}, errors.New("saml: certificate and private key do not match")
	}
	if key.N.BitLen() < 2048 {
		return KeyPair{}, errors.New("saml: private key must be at least 2048 bits")
	}
	return KeyPair{Certificate: leaf, Key: key}, nil
}

// ParseCertificates 解析 PEM 中的一个或多个证书；忽略非证书块。
func ParseCertificates(raw string) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := []byte(raw)
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remaining
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("saml: parse certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("saml: no certificate found in pem")
	}
	return certs, nil
}

// parsePrivateKey 支持 PKCS#8 与 PKCS#1 两种私钥编码。
func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("saml: no private key found in pem")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("saml: parse rsa private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("saml: parse pkcs8 private key: %w", err)
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("saml: private key must be RSA")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("saml: unsupported private key type %q", block.Type)
	}
}

// ValidateUsable 校验证书有效期；now 早于生效时间或晚于过期时间均视为不可用。
func ValidateUsable(cert *x509.Certificate, now time.Time) error {
	if cert == nil {
		return errors.New("saml: certificate is required")
	}
	if now.Before(cert.NotBefore) {
		return errors.New("saml: certificate is not valid yet")
	}
	if now.After(cert.NotAfter) {
		return errors.New("saml: certificate has expired")
	}
	return nil
}
