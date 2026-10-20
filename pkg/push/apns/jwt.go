package apns

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
)

const providerTokenLifetime = 50 * time.Minute

var (
	errNoPEMBlock     = errors.New("apns: private key is not PEM encoded")
	errNotECDSAPKey   = errors.New("apns: private key is not an EC private key")
	errUnsupportedKey = errors.New("apns: private key type is not supported")
	errP256Required   = errors.New("apns: private key must use P-256")
	errTokenSign      = errors.New("apns: sign provider token")
)

type tokenProvider struct {
	key    *ecdsa.PrivateKey
	keyID  string
	teamID string
	clock  clock.Clock

	mu      sync.Mutex
	cached  string
	expires time.Time
}

func newTokenProvider(key *ecdsa.PrivateKey, keyID, teamID string, clk clock.Clock) *tokenProvider {
	return &tokenProvider{key: key, keyID: keyID, teamID: teamID, clock: clk}
}

// token 返回缓存的 Provider JWT；有效期 50 分钟（Apple 上限 60 分钟）。
func (p *tokenProvider) token() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock.Now()
	if p.cached != "" && now.Before(p.expires) {
		return p.cached, nil
	}
	signed, err := signProviderToken(p.key, p.keyID, p.teamID, now)
	if err != nil {
		return "", err
	}
	p.cached = signed
	p.expires = now.Add(providerTokenLifetime)
	return signed, nil
}

func (p *tokenProvider) invalidate() {
	p.mu.Lock()
	p.cached = ""
	p.expires = time.Time{}
	p.mu.Unlock()
}

func signProviderToken(key *ecdsa.PrivateKey, keyID, teamID string, now time.Time) (string, error) {
	header := map[string]string{"alg": "ES256", "kid": keyID}
	claims := map[string]any{"iss": teamID, "iat": now.Unix()}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errTokenSign, err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errTokenSign, err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", fmt.Errorf("%w: %w", errTokenSign, err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parsePrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errNoPEMBlock
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errUnsupportedKey
		}
		return validateECKey(ecKey)
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return validateECKey(key)
	}
	return nil, errNotECDSAPKey
}

func validateECKey(key *ecdsa.PrivateKey) (*ecdsa.PrivateKey, error) {
	if key.Curve != elliptic.P256() {
		return nil, errP256Required
	}
	return key, nil
}
