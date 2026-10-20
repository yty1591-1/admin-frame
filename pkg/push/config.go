package push

import (
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	"github.com/admin-frame/admin-frame/pkg/httpclient"
)

const (
	defaultTimeout = 5 * time.Second
	maxTimeout     = 30 * time.Second
)

// Config 是 Open 的统一配置；Driver 必填，其余零值经 Normalized 补默认。
//
// 本结构不读取文件或环境变量；由 internal/platform 完成 YAML/env 映射（CFG-07）。
type Config struct {
	// Driver 为驱动名，须已通过 blank-import 注册。
	Driver string
	// Timeout 是单次发送兜底超时；0 归一为 5s，超过 30s 视为非法。
	Timeout time.Duration
	// Observer 接收发送事件；可为 nil。
	Observer Observer
	// Clock 为注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
	// HTTPClient 是出站客户端；nil 时由驱动按 Timeout 创建。
	// 驱动传入的客户端必须关闭自动重试，避免与编排层重试叠加。
	HTTPClient *httpclient.Client
	// APNS 是 APNs 驱动配置；仅 Driver=="apns" 时读取。
	APNS APNSConfig
	// FCM 是 FCM 驱动配置；仅 Driver=="fcm" 时读取。
	FCM FCMConfig
}

// APNSConfig 是 APNs Token 认证配置。
//
// PrivateKey 可传 PEM 内容或文件路径；生产应由 KMS/Secret Manager 注入。
type APNSConfig struct {
	// Endpoint 默认 https://api.push.apple.com；沙箱为 https://api.sandbox.push.apple.com。
	Endpoint string
	// Topic 是 App Bundle ID，必填。
	Topic string
	// KeyID 是 APNs Auth Key 的 Key ID，必填。
	KeyID string
	// TeamID 是 Apple Developer Team ID，必填。
	TeamID string
	// PrivateKey 是 .p8 的 PEM 内容或文件路径，必填。
	PrivateKey string
	// PushType 为空时按 alert 处理；可选 alert/background/voip 等。
	PushType string
}

// FCMConfig 是 Firebase Cloud Messaging HTTP v1 配置。
//
// CredentialsJSON 可传 Service Account JSON 内容或文件路径。
type FCMConfig struct {
	// Endpoint 默认 https://fcm.googleapis.com。
	Endpoint string
	// ProjectID 为空时使用 Service Account 中的 project_id。
	ProjectID string
	// CredentialsJSON 是 Service Account JSON 内容或文件路径，必填。
	CredentialsJSON string
}

// Normalized 返回补全默认值后的副本；不修改接收者。
func (c Config) Normalized() Config {
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.Clock == nil {
		c.Clock = clock.RealClock()
	}
	return c
}

// Validate 校验通用配置；驱动专属字段由各驱动在 Open 时校验。
func (c Config) Validate() error {
	c = c.Normalized()
	if strings.TrimSpace(c.Driver) == "" {
		return fmt.Errorf("%w: driver is required", ErrInvalidConfig)
	}
	if c.Timeout <= 0 || c.Timeout > maxTimeout {
		return fmt.Errorf("%w: timeout must be in (0,%s]", ErrInvalidConfig, maxTimeout)
	}
	return nil
}
