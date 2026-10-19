package db

import (
	"net/url"
	"strings"
)

// RedactDSN 脱敏连接串中的口令，便于日志与错误消息（SEC-05）。
//
// 支持常见形式：
//   - URL：postgres://user:secret@host/db → user:***@host
//   - key=value：password=secret / pwd=secret → ***
func RedactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		if u.User != nil {
			name := u.User.Username()
			if _, has := u.User.Password(); has {
				u.User = url.UserPassword(name, "***")
			} else {
				u.User = url.User(name)
			}
		}
		return u.String()
	}
	return redactKeyValueDSN(dsn)
}

func redactKeyValueDSN(dsn string) string {
	parts := strings.FieldsFunc(dsn, func(r rune) bool {
		return r == ' ' || r == ';'
	})
	for i, p := range parts {
		lower := strings.ToLower(p)
		for _, key := range []string{"password=", "passwd=", "pwd="} {
			if strings.HasPrefix(lower, key) {
				eq := strings.IndexByte(p, '=')
				if eq >= 0 {
					parts[i] = p[:eq+1] + "***"
				}
				break
			}
		}
	}
	// 尽量保留原分隔风格：含分号则用分号，否则空格。
	sep := " "
	if strings.Contains(dsn, ";") {
		sep = ";"
	}
	return strings.Join(parts, sep)
}
