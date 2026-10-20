package fcm

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/pkg/push"
)

const defaultTokenURI = "https://oauth2.googleapis.com/token" //nolint:gosec // 公开的 Google OAuth 端点，非凭据。

var (
	errNoServiceAccountKey = errors.New("fcm: service account private key is not PEM encoded")
	errServiceAccountKey   = errors.New("fcm: service account private key is invalid")
)

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

func loadServiceAccount(value string) (*serviceAccount, *rsa.PrivateKey, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil, fmt.Errorf("%w: fcm credentials are required", push.ErrInvalidConfig)
	}
	raw, err := readSecret(value)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: read fcm credentials", push.ErrInvalidConfig)
	}
	var account serviceAccount
	if err := json.Unmarshal(raw, &account); err != nil {
		return nil, nil, fmt.Errorf("%w: decode fcm credentials", push.ErrInvalidConfig)
	}
	if account.ClientEmail == "" || account.PrivateKey == "" {
		return nil, nil, fmt.Errorf("%w: fcm service account is incomplete", push.ErrInvalidConfig)
	}
	if account.TokenURI == "" {
		account.TokenURI = defaultTokenURI
	}
	key, err := parseRSAPrivateKey([]byte(account.PrivateKey))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", push.ErrInvalidConfig, err)
	}
	return &account, key, nil
}

func readSecret(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), nil
	}
	return os.ReadFile(trimmed) // #nosec G304 -- 配置显式指定的凭据文件路径。
}

func parseRSAPrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errNoServiceAccountKey
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errServiceAccountKey
		}
		return rsaKey, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errServiceAccountKey
}
