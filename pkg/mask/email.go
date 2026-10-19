package mask

import "strings"

// Email 脱敏邮箱：a***@example.com；无 @ 时 Keep(1,0)。
func Email(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return Keep(s, 1, 0)
	}
	local := s[:at]
	domain := s[at:]
	localRunes := []rune(local)
	switch len(localRunes) {
	case 0:
		return "***" + domain
	case 1:
		return string(localRunes[0]) + "***" + domain
	default:
		return string(localRunes[0]) + "***" + domain
	}
}
