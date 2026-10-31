package domain

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
)

// 设备画像取值；仅用于展示，禁止参与鉴权或风控判定。
const (
	DeviceTypeDesktop = "desktop"
	DeviceTypeMobile  = "mobile"
	DeviceTypeTablet  = "tablet"
)

// DeviceInfo 是登录请求派生出的可展示设备画像。
//
// IP / User-Agent 原文禁止落库：这里只保留粗粒度枚举与脱敏 IP，
// 用于让用户分辨自己的登录设备，同时避免泄露可直接识别个人的原始信息。
type DeviceInfo struct {
	// Type 设备形态：desktop / mobile / tablet；无法识别为空。
	Type string
	// OS 操作系统：windows / macos / ios / android / linux / chromeos；无法识别为空。
	OS string
	// Browser 浏览器：chrome / edge / safari / firefox / wechat / opera；无法识别为空。
	Browser string
	// IPMasked 脱敏客户端 IP：IPv4 保留前两段，IPv6 保留前四组；无法解析为空。
	IPMasked string
}

// IsEmpty 报告画像是否没有任何可用字段。
func (d DeviceInfo) IsEmpty() bool {
	return d.Type == "" && d.OS == "" && d.Browser == "" && d.IPMasked == ""
}

// MergeMissing 仅补齐当前画像缺失的字段，不覆盖已有值。
//
// 用于历史会话：首次刷新可能只拿到 IP，后续刷新再补全浏览器 / 系统。
func (d *DeviceInfo) MergeMissing(other DeviceInfo) {
	if d.Type == "" {
		d.Type = other.Type
	}
	if d.OS == "" {
		d.OS = other.OS
	}
	if d.Browser == "" {
		d.Browser = other.Browser
	}
	if d.IPMasked == "" {
		d.IPMasked = other.IPMasked
	}
}

// Label 返回可直接展示的设备名，例如 "Chrome · Windows"；无可用信息时返回空串。
func (d DeviceInfo) Label() string {
	browser := browserDisplayName(d.Browser)
	osName := osDisplayName(d.OS)
	switch {
	case browser != "" && osName != "":
		return browser + " · " + osName
	case browser != "":
		return browser
	default:
		return osName
	}
}

// ParseDeviceInfo 从 User-Agent 与客户端 IP 派生设备画像。
func ParseDeviceInfo(userAgent, clientIP string) DeviceInfo {
	return DeviceInfo{
		Type:     parseDeviceType(userAgent),
		OS:       parseDeviceOS(userAgent),
		Browser:  parseDeviceBrowser(userAgent),
		IPMasked: MaskIP(clientIP),
	}
}

// MaskIP 脱敏 IP：IPv4 保留前两段（1.2.x.x），IPv6 保留前四组；无法解析返回空串。
//
// 回环（127.0.0.1 / ::1）与未指定地址（0.0.0.0 / ::）不具备区分设备的含义，
// 脱敏后只会得到 0 段（如 0:0:0:0:x:x:x:x），因此直接不返回，由前端省略展示。
func MaskIP(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.x.x", v4[0], v4[1])
	}
	v6 := ip.To16()
	if v6 == nil {
		return ""
	}
	groups := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		groups = append(groups, fmt.Sprintf("%x", binary.BigEndian.Uint16(v6[i*2:i*2+2])))
	}
	return strings.Join(groups, ":") + ":x:x:x:x"
}

// NormalizeMaskedIP 清理历史数据中由回环 / 未指定地址派生的无效脱敏值。
//
// 旧版本未过滤 127.0.0.1 / ::1，落库后会得到 127.0.x.x 或 0:0:0:0:x:x:x:x；
// 这些值不具设备区分意义，统一清空，由前端省略展示。
func NormalizeMaskedIP(masked string) string {
	masked = strings.TrimSpace(masked)
	if masked == "0.0.x.x" || masked == "127.0.x.x" || strings.HasPrefix(masked, "0:0:0:0:") {
		return ""
	}
	return masked
}

// Sanitized 返回清理无效脱敏 IP 后的设备画像，用于读取历史会话时兜底。
func (d DeviceInfo) Sanitized() DeviceInfo {
	d.IPMasked = NormalizeMaskedIP(d.IPMasked)
	return d
}

// parseDeviceBrowser 识别浏览器；顺序保证微信内置浏览器、Edge、Opera 不被 Chrome 抢占。
func parseDeviceBrowser(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "micromessenger"):
		return "wechat"
	case strings.Contains(ua, "edg/"), strings.Contains(ua, "edge/"):
		return "edge"
	case strings.Contains(ua, "opr/"):
		return "opera"
	case strings.Contains(ua, "chrome/"), strings.Contains(ua, "crios/"):
		return "chrome"
	case strings.Contains(ua, "firefox/"), strings.Contains(ua, "fxios/"):
		return "firefox"
	case strings.Contains(ua, "safari/"):
		return "safari"
	default:
		return ""
	}
}

// parseDeviceOS 识别操作系统；iPadOS 13+ 伪装成 Macintosh，需要结合 Mobile 标识。
func parseDeviceOS(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "windows"):
		return "windows"
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"), strings.Contains(ua, "ipod"):
		return "ios"
	case strings.Contains(ua, "macintosh") && strings.Contains(ua, "mobile/"):
		return "ios"
	case strings.Contains(ua, "android"):
		return "android"
	case strings.Contains(ua, "cros"):
		return "chromeos"
	case strings.Contains(ua, "mac os x"), strings.Contains(ua, "macintosh"):
		return "macos"
	case strings.Contains(ua, "linux"):
		return "linux"
	default:
		return ""
	}
}

// parseDeviceType 识别设备形态；Android 平板通常缺少 Mobile 标识。
func parseDeviceType(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "ipad"), strings.Contains(ua, "tablet"):
		return DeviceTypeTablet
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipod"):
		return DeviceTypeMobile
	case strings.Contains(ua, "macintosh") && strings.Contains(ua, "mobile/"):
		return DeviceTypeTablet
	case strings.Contains(ua, "android") && !strings.Contains(ua, "mobile"):
		return DeviceTypeTablet
	case strings.Contains(ua, "mobile"):
		return DeviceTypeMobile
	case parseDeviceOS(userAgent) != "":
		return DeviceTypeDesktop
	default:
		return ""
	}
}

// browserDisplayName 把内部枚举转换为展示名。
func browserDisplayName(browser string) string {
	switch browser {
	case "chrome":
		return "Chrome"
	case "edge":
		return "Edge"
	case "safari":
		return "Safari"
	case "firefox":
		return "Firefox"
	case "wechat":
		return "WeChat"
	case "opera":
		return "Opera"
	default:
		return ""
	}
}

// osDisplayName 把内部枚举转换为展示名。
func osDisplayName(osName string) string {
	switch osName {
	case "windows":
		return "Windows"
	case "macos":
		return "macOS"
	case "ios":
		return "iOS"
	case "android":
		return "Android"
	case "linux":
		return "Linux"
	case "chromeos":
		return "ChromeOS"
	default:
		return ""
	}
}
