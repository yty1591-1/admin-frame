package mask

import (
	"regexp"
	"strings"
)

// 预编译密钥/口令赋值模式（日志自由文本）。
var (
	reKVSecret = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|api[_-]?key|access[_-]?token|private[_-]?key)\s*[=:]\s*([^\s&;,"']+)`)
	reBearer   = regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9\-._~+/]+=*)`)
	rePEM      = regexp.MustCompile(`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
)

// RedactSecrets 擦除自由文本中的 password=/Bearer/PEM 等密钥形态（AUD-08 / SEC-05）。
func RedactSecrets(s string) string {
	if s == "" {
		return ""
	}
	out := rePEM.ReplaceAllString(s, "-----BEGIN PRIVATE KEY-----[REDACTED]-----END PRIVATE KEY-----")
	out = reBearer.ReplaceAllStringFunc(out, func(m string) string {
		parts := strings.Fields(m)
		if len(parts) < 2 {
			return "Bearer [REDACTED]"
		}
		return parts[0] + " " + Token(parts[1])
	})
	out = reKVSecret.ReplaceAllString(out, "${1}=***")
	return out
}
