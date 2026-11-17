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
	// MinLen/MaxLen 始终指解码后字节数；不得按编码串字符数截断，
	// 否则 hex/base64 会破坏长度与 MatchPlaintext 对偶。
	var body string
	switch encoding {
	case "hex":
		body = hex.EncodeToString(raw)
	case "raw":
		body = string(raw)
	default:
		body = base64.RawURLEncoding.EncodeToString(raw)
	}
	return r.Prefix + body, nil
}

// MatchPlaintext 校验明文是否满足规则（CC-R-14：Create/Rotate 入库前）。
func (r SecretRule) MatchPlaintext(value string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	raw := strings.TrimSpace(value)
	if raw == "" {
		return fmt.Errorf("configcenter: secret plaintext is empty")
	}
	prefix := r.Prefix
	if prefix != "" {
		if !strings.HasPrefix(raw, prefix) {
			return fmt.Errorf("configcenter: secret plaintext missing required prefix")
		}
		raw = strings.TrimPrefix(raw, prefix)
	}
	encoding := strings.ToLower(strings.TrimSpace(r.Encoding))
	if encoding == "" {
		encoding = "base64url"
	}
	var decoded []byte
	switch encoding {
	case "hex":
		b, err := hex.DecodeString(raw)
		if err != nil {
			return fmt.Errorf("configcenter: secret plaintext is not valid hex")
		}
		decoded = b
	case "raw":
		decoded = []byte(raw)
	default:
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			// 兼容带 padding 的标准 base64url。
			b, err = base64.URLEncoding.DecodeString(raw)
			if err != nil {
				return fmt.Errorf("configcenter: secret plaintext is not valid base64url")
			}
		}
		decoded = b
	}
	n := len(decoded)
	if r.MinLen > 0 && n < r.MinLen {
		return fmt.Errorf("configcenter: secret plaintext length %d is below min %d", n, r.MinLen)
	}
	if r.MaxLen > 0 && n > r.MaxLen {
		return fmt.Errorf("configcenter: secret plaintext length %d exceeds max %d", n, r.MaxLen)
	}
	return nil
}
