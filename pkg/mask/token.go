package mask

import (
	"strings"
)

// Token 脱敏会话 / API Token：长度 ≥ 12 时保留前 4 后 4，否则全掩。
func Token(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) < 12 {
		return All(s)
	}
	return Keep(s, 4, 4)
}

// Authorization 脱敏 Authorization 头：Bearer <token> → Bearer ****…。
func Authorization(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return prefix + Token(strings.TrimSpace(s[len(prefix):]))
	}
	return Token(s)
}
