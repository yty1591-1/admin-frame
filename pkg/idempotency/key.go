package idempotency

import (
	"net/http"
	"strings"
	"unicode"
)

const (
	// Header 标准幂等键请求头（API-11）。
	Header = "Idempotency-Key"
	// MaxKeyLength 键最大长度（对齐常见支付网关上限）。
	MaxKeyLength = 255
)

// ValidateKey 校验客户端幂等键：非空、长度 ≤ MaxKeyLength、可打印 ASCII（不含空格控制符）。
func ValidateKey(key string) error {
	if key == "" {
		return ErrMissingKey
	}
	if len(key) > MaxKeyLength {
		return ErrInvalidKey
	}
	for _, r := range key {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return ErrInvalidKey
		}
	}
	return nil
}

// KeyFromRequest 读取并校验 Idempotency-Key；header 空则用 Header。
func KeyFromRequest(r *http.Request, header string) (string, error) {
	if r == nil {
		return "", ErrMissingKey
	}
	if header == "" {
		header = Header
	}
	key := strings.TrimSpace(r.Header.Get(header))
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// CompositeKey 将作用域与客户端键拼成存储键，避免跨用户/商户碰撞。
// scope 为空时仅返回 clientKey。
func CompositeKey(scope, clientKey string) string {
	if scope == "" {
		return clientKey
	}
	return scope + ":" + clientKey
}
