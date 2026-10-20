package email

import (
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
)

const (
	defaultTimeout     = 10 * time.Second
	maxTimeout         = 60 * time.Second
	maxRecipientsHard  = 500
	maxAttachmentsHard = 100
	maxAttachmentHard  = 25 << 20
	maxMessageHard     = 40 << 20
)

// Config 是 Open 的统一配置；Driver 与 From 必填，其余零值经 Normalized 补默认。
//
// 本结构不读取文件或环境变量；由 internal/platform 完成 YAML/env 映射（CFG-07）。
type Config struct {
	// Driver 为驱动名，须已通过 blank-import 注册。
	Driver string
	// Timeout 是单次发送兜底超时；0 归一为 10s，超过 60s 视为非法。
	Timeout time.Duration
	// From 是默认发件人；Message.From 为空时使用。
	From Address
	// MaxRecipients 是 To 与 Cc+Bcc 各自上限；0 归一为 50。
	MaxRecipients int
	// MaxAttachments 是附件个数上限；0 归一为 10。
	MaxAttachments int
	// MaxAttachmentBytes 是单附件上限；0 归一为 5 MiB。
	MaxAttachmentBytes int64
	// MaxMessageBytes 是整封邮件上限；0 归一为 10 MiB。
	MaxMessageBytes int64
	// AllowExecutableAttachments 为 true 时允许可执行附件；默认拒绝。
	AllowExecutableAttachments bool
	// Observer 接收发送事件；可为 nil。
	Observer Observer
	// Clock 为注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
	// SMTP 是 SMTP 驱动配置；仅 Driver=="smtp" 时读取。
	SMTP SMTPConfig
}

// SMTPConfig 是 SMTP 连接与认证配置。
type SMTPConfig struct {
	// Host 是 SMTP 服务器主机名，必填。
	Host string
	// Port 是端口；0 归一为 587。
	Port int
	// Encryption 为 starttls（默认）、tls（隐式 TLS，常用 465）或 none。
	// none 仅在 AllowInsecure=true 时允许，生产环境禁止。
	Encryption string
	// Auth 为 plain（默认）、login 或 none。
	Auth string
	// Username 与 Password 为认证凭据；Password 只应来自环境变量或 KMS。
	Username string
	Password string
	// HELOName 是 EHLO 名称；空则使用主机名。
	HELOName string
	// TLSMinVersion 为 "1.2"（默认）或 "1.3"。
	TLSMinVersion string
	// AllowInsecure 显式允许明文 SMTP 或无认证；仅限本地/测试。
	AllowInsecure bool
}

// Normalized 返回补全默认值后的副本；不修改接收者。
func (c Config) Normalized() Config {
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.Clock == nil {
		c.Clock = clock.RealClock()
	}
	if c.MaxRecipients <= 0 {
		c.MaxRecipients = defaultMaxRecipients
	}
	if c.MaxAttachments <= 0 {
		c.MaxAttachments = defaultMaxAttachments
	}
	if c.MaxAttachmentBytes <= 0 {
		c.MaxAttachmentBytes = defaultMaxAttachmentBytes
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = defaultMaxMessageBytes
	}
	if c.SMTP.Port <= 0 {
		c.SMTP.Port = 587
	}
	if strings.TrimSpace(c.SMTP.Encryption) == "" {
		c.SMTP.Encryption = "starttls"
	}
	if strings.TrimSpace(c.SMTP.Auth) == "" {
		if c.SMTP.Username != "" {
			c.SMTP.Auth = "plain"
		} else {
			c.SMTP.Auth = "none"
		}
	}
	if strings.TrimSpace(c.SMTP.TLSMinVersion) == "" {
		c.SMTP.TLSMinVersion = "1.2"
	}
	return c
}

// Limits 将配置中的上限转换为消息校验上限。
func (c Config) Limits() Limits {
	c = c.Normalized()
	return Limits{
		MaxRecipients:              c.MaxRecipients,
		MaxAttachments:             c.MaxAttachments,
		MaxAttachmentBytes:         c.MaxAttachmentBytes,
		MaxMessageBytes:            c.MaxMessageBytes,
		AllowExecutableAttachments: c.AllowExecutableAttachments,
	}
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
	if c.From.Email == "" {
		return fmt.Errorf("%w: from address is required", ErrInvalidConfig)
	}
	if err := validateAddress(c.From); err != nil {
		return fmt.Errorf("%w: from address: %w", ErrInvalidConfig, err)
	}
	if c.MaxRecipients > maxRecipientsHard {
		return fmt.Errorf("%w: max recipients exceeds %d", ErrInvalidConfig, maxRecipientsHard)
	}
	if c.MaxAttachments > maxAttachmentsHard {
		return fmt.Errorf("%w: max attachments exceeds %d", ErrInvalidConfig, maxAttachmentsHard)
	}
	if c.MaxAttachmentBytes > maxAttachmentHard {
		return fmt.Errorf("%w: max attachment bytes exceeds %d", ErrInvalidConfig, maxAttachmentHard)
	}
	if c.MaxMessageBytes > maxMessageHard || c.MaxMessageBytes < c.MaxAttachmentBytes {
		return fmt.Errorf("%w: max message bytes is invalid", ErrInvalidConfig)
	}
	return nil
}
