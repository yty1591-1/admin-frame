package domain

import "testing"

func TestParseDeviceInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		clientIP  string
		want      DeviceInfo
	}{
		{
			name:      "chrome on windows",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
			clientIP:  "203.0.113.42",
			want: DeviceInfo{
				Type: DeviceTypeDesktop, OS: "windows", Browser: "chrome", IPMasked: "203.0.x.x",
			},
		},
		{
			name:      "edge is not chrome",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
			clientIP:  "198.51.100.7",
			want: DeviceInfo{
				Type: DeviceTypeDesktop, OS: "windows", Browser: "edge", IPMasked: "198.51.x.x",
			},
		},
		{
			name:      "wechat built-in browser",
			userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.49",
			clientIP:  "2001:db8:1:2::1",
			want: DeviceInfo{
				Type: DeviceTypeMobile, OS: "ios", Browser: "wechat", IPMasked: "2001:db8:1:2:x:x:x:x",
			},
		},
		{
			name:      "android tablet without mobile token",
			userAgent: "Mozilla/5.0 (Linux; Android 14; SM-X510) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
			clientIP:  "192.168.10.1",
			want: DeviceInfo{
				Type: DeviceTypeTablet, OS: "android", Browser: "chrome", IPMasked: "192.168.x.x",
			},
		},
		{
			name:      "ipados safari reports macintosh with mobile",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
			clientIP:  "not-an-ip",
			want: DeviceInfo{
				Type: DeviceTypeTablet, OS: "ios", Browser: "safari", IPMasked: "",
			},
		},
		{
			name:      "unknown user agent keeps ip",
			userAgent: "",
			clientIP:  "10.0.0.1",
			want: DeviceInfo{
				Type: "", OS: "", Browser: "", IPMasked: "10.0.x.x",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDeviceInfo(tt.userAgent, tt.clientIP)
			if got != tt.want {
				t.Fatalf("ParseDeviceInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDeviceInfoLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   DeviceInfo
		want string
	}{
		{name: "browser and os", in: DeviceInfo{Browser: "chrome", OS: "windows"}, want: "Chrome · Windows"},
		{name: "browser only", in: DeviceInfo{Browser: "safari"}, want: "Safari"},
		{name: "os only", in: DeviceInfo{OS: "linux"}, want: "Linux"},
		{name: "empty", in: DeviceInfo{IPMasked: "1.2.x.x"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.in.Label(); got != tt.want {
				t.Fatalf("Label() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMaskIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
	}{
		{raw: "203.0.113.42", want: "203.0.x.x"},
		{raw: " 192.168.1.1 ", want: "192.168.x.x"},
		{raw: "2001:db8:abcd:12::1", want: "2001:db8:abcd:12:x:x:x:x"},
		{raw: "::1", want: ""},
		{raw: "127.0.0.1", want: ""},
		{raw: "0.0.0.0", want: ""},
		{raw: "::", want: ""},
		{raw: "", want: ""},
		{raw: "invalid", want: ""},
	}
	for _, tt := range tests {
		if got := MaskIP(tt.raw); got != tt.want {
			t.Fatalf("MaskIP(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestNormalizeMaskedIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
	}{
		{raw: "0:0:0:0:x:x:x:x", want: ""},
		{raw: "127.0.x.x", want: ""},
		{raw: "0.0.x.x", want: ""},
		{raw: "203.0.x.x", want: "203.0.x.x"},
		{raw: "2001:db8:abcd:12:x:x:x:x", want: "2001:db8:abcd:12:x:x:x:x"},
		{raw: "", want: ""},
	}
	for _, tt := range tests {
		if got := NormalizeMaskedIP(tt.raw); got != tt.want {
			t.Fatalf("NormalizeMaskedIP(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
