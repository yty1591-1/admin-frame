package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientIPResolver 覆盖可信代理、XFF 伪造、端口与 IPv6 规范化。
func TestClientIPResolver(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8", "fd00::/8"})
	if err != nil {
		t.Fatalf("NewClientIPResolver() error = %v", err)
	}
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{name: "direct ipv4 with port", remoteAddr: "203.0.113.7:54321", want: "203.0.113.7"},
		{name: "trusted proxy single hop", remoteAddr: "10.1.2.3:80", xff: "198.51.100.9", want: "198.51.100.9"},
		{
			name:       "trusted proxy keeps rightmost untrusted",
			remoteAddr: "10.1.2.3:80", xff: "1.2.3.4, 198.51.100.9", want: "198.51.100.9",
		},
		{
			name:       "skip multiple trusted proxies",
			remoteAddr: "10.1.2.3:80", xff: "198.51.100.9, 10.4.5.6, 10.7.8.9", want: "198.51.100.9",
		},
		{name: "untrusted source ignores forged xff", remoteAddr: "203.0.113.7:54321", xff: "1.2.3.4", want: "203.0.113.7"},
		{name: "trusted proxy without xff", remoteAddr: "10.1.2.3:80", want: "10.1.2.3"},
		{name: "ipv6 normalized", remoteAddr: "[2001:db8::1]:443", want: "2001:db8::1"},
		{
			name: "trusted ipv6 proxy", remoteAddr: "[fd00::1]:443", xff: "2001:db8::2", want: "2001:db8::2",
		},
		{name: "empty xff entries ignored", remoteAddr: "10.1.2.3:80", xff: " , 198.51.100.9", want: "198.51.100.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := resolver.ClientIP(req); got != tc.want {
				t.Fatalf("ClientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClientIPResolverNil 验证未配置可信代理时永不信任 XFF。
func TestClientIPResolverNil(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := (*ClientIPResolver)(nil).ClientIP(req); got != "203.0.113.7" {
		t.Fatalf("nil resolver ClientIP() = %q, want remote addr", got)
	}
}

// TestNewClientIPResolverRejectsInvalid 验证非法 CIDR 在装配期失败。
func TestNewClientIPResolverRejectsInvalid(t *testing.T) {
	if _, err := NewClientIPResolver([]string{"not-a-cidr"}); err == nil {
		t.Fatalf("invalid CIDR must be rejected")
	}
}
