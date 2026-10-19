package mask

import (
	"strings"
	"unicode"
)

// Phone 脱敏中国大陆 11 位手机号：138****5678。
// 非 11 位数字时按 Keep(3,4) 回落；过短则 All。
func Phone(s string) string {
	digits := digitsOnly(s)
	if len(digits) == 11 && digits[0] == '1' {
		return digits[:3] + "****" + digits[7:]
	}
	if len(digits) >= 7 {
		return Keep(digits, 3, 4)
	}
	if digits != "" {
		return All(digits)
	}
	return All(strings.TrimSpace(s))
}

func digitsOnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
