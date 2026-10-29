package httpx

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ClientIPResolver 按可信代理链解析真实客户端 IP（IDN-P1-04）。
//
// 规则：
//   - 只有来自 http.trusted_proxies 的请求才解析 X-Forwarded-For；
//   - 从右向左跳过可信代理，取第一个不可信地址，防止伪造最左段；
//   - 无 XFF 或来源不可信时使用 RemoteAddr，并去掉端口；
//   - IPv6 统一为规范文本，保证同一地址的不同连接共享限流键。
//
// 零值 / nil 解析器等价于「不信任任何代理」，只使用 RemoteAddr。
type ClientIPResolver struct {
	trusted []*net.IPNet
}

// NewClientIPResolver 解析可信代理 CIDR 列表；空列表表示不信任任何代理。
func NewClientIPResolver(cidrs []string) (*ClientIPResolver, error) {
	resolver := &ClientIPResolver{}
	for _, raw := range cidrs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		_, network, err := net.ParseCIDR(trimmed)
		if err != nil {
			return nil, fmt.Errorf("httpx: invalid trusted proxy CIDR: %w", err)
		}
		resolver.trusted = append(resolver.trusted, network)
	}
	return resolver, nil
}

// ClientIP 解析请求的真实客户端 IP；req 为 nil 时返回空串。
func (r *ClientIPResolver) ClientIP(req *http.Request) string {
	if req == nil {
		return ""
	}
	remote := normalizeHost(req.RemoteAddr)
	if !r.trustedIP(remote) {
		return remote
	}
	// 来自可信代理：从右向左找到第一个不可信地址，即真实客户端。
	parts := strings.Split(req.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := normalizeHost(strings.TrimSpace(parts[i]))
		if candidate == "" || r.trustedIP(candidate) {
			continue
		}
		return candidate
	}
	// 全部条目均不可用（空、非法或都是可信代理）：回退直连地址。
	return remote
}

// trustedIP 报告地址是否属于可信代理；空列表或非法地址返回 false。
func (r *ClientIPResolver) trustedIP(raw string) bool {
	if r == nil || len(r.trusted) == 0 || raw == "" {
		return false
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return false
	}
	for _, network := range r.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// normalizeHost 去掉端口与方括号，并把 IPv6 规范化为字符串形式。
func normalizeHost(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return value
}
