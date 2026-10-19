package mask

import "strings"

// Name 脱敏姓名（按 rune）：单字 → *；两字 → 首+*；三字及以上 → 首 + 中间* + 末。
func Name(s string) string {
	runes := []rune(strings.TrimSpace(s))
	switch len(runes) {
	case 0:
		return ""
	case 1:
		return string(MaskChar)
	case 2:
		return string(runes[0]) + string(MaskChar)
	default:
		return Keep(string(runes), 1, 1)
	}
}
