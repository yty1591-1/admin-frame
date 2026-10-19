package storage

import (
	"fmt"
	"strings"
)

const (
	// MaxKeyLength 对象键最大长度。
	MaxKeyLength = 1024
)

// Join 用 "/" 拼接键段并校验；段内不得含 "/"、"\" 或 ".."。
func Join(parts ...string) (string, error) {
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(p, "/")
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, `/\`) || p == "." || p == ".." || strings.Contains(p, "..") {
			return "", fmt.Errorf("%w: invalid key segment %q", ErrInvalidKey, p)
		}
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		return "", ErrInvalidKey
	}
	key := strings.Join(cleaned, "/")
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// MustJoin 同 Join，失败时 panic（静态键装配用）。
func MustJoin(parts ...string) string {
	k, err := Join(parts...)
	if err != nil {
		panic(err)
	}
	return k
}

// ValidateKey 校验对象键：非空、长度受限、无绝对路径/穿越/控制字符。
func ValidateKey(key string) error {
	if key == "" {
		return ErrInvalidKey
	}
	if len(key) > MaxKeyLength {
		return fmt.Errorf("%w: key too long", ErrInvalidKey)
	}
	if strings.HasPrefix(key, "/") || strings.Contains(key, `\`) {
		return fmt.Errorf("%w: absolute or backslash key", ErrInvalidKey)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: path traversal", ErrInvalidKey)
		}
		for _, r := range seg {
			if r < 32 || r == 127 {
				return fmt.Errorf("%w: control character", ErrInvalidKey)
			}
		}
	}
	return nil
}
