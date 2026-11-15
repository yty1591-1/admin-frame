package domain

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// SecretRule 是敏感键的生成与格式约束（ADR-0014 决策 12）。
type SecretRule struct {
	MinLen       int
	MaxLen       int
	Encoding     string // base64url / hex / raw
	Algorithm    string // 描述性；生成侧目前仅 random
	Prefix       string
	AutoGenerate bool
}

// Validate 校验规则自身。
func (r SecretRule) Validate() error {
	if r.MinLen < 0 || r.MaxLen < 0 || (r.MaxLen > 0 && r.MinLen > r.MaxLen) {
		return fmt.Errorf("configcenter: invalid secret_rule length bounds")
	}
	switch strings.ToLower(strings.TrimSpace(r.Encoding)) {
	case "", "base64url", "hex", "raw":
	default:
		return fmt.Errorf("configcenter: unsupported secret_rule encoding %q", r.Encoding)
	}
	return nil
}

// Generate 按规则生成明文；仅 AutoGenerate=true 时可用。
func (r SecretRule) Generate() (string, error) {
	if !r.AutoGenerate {
		return "", fmt.Errorf("configcenter: secret_rule auto_generate is false")
	}
	if err := r.Validate(); err != nil {
		return "", err
	}
	length := r.MinLen
	if length <= 0 {
		length = 32
	}
	if r.MaxLen > 0 && length > r.MaxLen {
		length = r.MaxLen
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	encoding := strings.ToLower(strings.TrimSpace(r.Encoding))
	if encoding == "" {
		encoding = "base64url"
	}
	var body string
	switch encoding {
	case "hex":
		body = hex.EncodeToString(raw)
		if r.MaxLen > 0 && len(body) > r.MaxLen {
			body = body[:r.MaxLen]
		}
	case "raw":
		body = string(raw)
	default:
		body = base64.RawURLEncoding.EncodeToString(raw)
		if r.MaxLen > 0 && len(body) > r.MaxLen {
			body = body[:r.MaxLen]
		}
	}
	return r.Prefix + body, nil
}
