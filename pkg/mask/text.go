package mask

import (
	"regexp"
)

var (
	reMobile  = regexp.MustCompile(`(?:\+?86[-\s]?)?(1[3-9]\d{9})`)
	reIDCard  = regexp.MustCompile(`\b([1-9]\d{5}(?:\d{2}|\d{4})(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx])\b`)
	reEmail   = regexp.MustCompile(`\b([A-Za-z0-9._%+\-]+)@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})\b`)
	reBankish = regexp.MustCompile(`\b(\d{13,19})\b`)
)

// Text 对自由文本做启发式 PII/密钥脱敏，适合日志 message / 错误详情。
// 顺序：密钥 → 证件 → 手机 → 邮箱 → 疑似卡号。
func Text(s string) string {
	if s == "" {
		return ""
	}
	out := RedactSecrets(s)
	out = reIDCard.ReplaceAllStringFunc(out, func(m string) string {
		return IDCard(m)
	})
	out = reMobile.ReplaceAllStringFunc(out, func(m string) string {
		return Phone(m)
	})
	out = reEmail.ReplaceAllStringFunc(out, func(m string) string {
		return Email(m)
	})
	out = reBankish.ReplaceAllStringFunc(out, func(m string) string {
		// 避免把已脱敏的 11 位手机残留再当卡号；仅处理更长数字串
		if len(digitsOnly(m)) < 13 {
			return m
		}
		return BankCard(m)
	})
	return out
}
