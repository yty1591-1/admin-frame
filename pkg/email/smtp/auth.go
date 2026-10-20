package smtp

import (
	"errors"
	netsmtp "net/smtp"
	"strings"
)

var (
	errLoginHost      = errors.New("smtp: unexpected server name")
	errLoginChallenge = errors.New("smtp: unexpected auth challenge")
)

// loginAuth 实现 LOGIN 认证（部分企业邮箱仅支持该机制）。
type loginAuth struct {
	username string
	password string
	host     string
}

// Start 实现 smtp.Auth，返回 LOGIN 机制名。
func (a *loginAuth) Start(server *netsmtp.ServerInfo) (mechanism string, initialResponse []byte, err error) {
	if server.Name != a.host {
		return "", nil, errLoginHost
	}
	return "LOGIN", nil, nil
}

// Next 实现 smtp.Auth，按服务端挑战返回用户名或密码。
func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(string(fromServer))
	switch {
	case strings.Contains(prompt, "username"):
		return []byte(a.username), nil
	case strings.Contains(prompt, "password"):
		return []byte(a.password), nil
	default:
		return nil, errLoginChallenge
	}
}
