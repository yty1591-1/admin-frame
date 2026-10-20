package sms

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
	// AllowRawContent 允许使用 Message.Content 裸内容；生产建议保持 false。
	AllowRawContent bool
	// Observer 接收发送事件；可为 nil。
	Observer Observer
	// Clock 为注入时钟；nil 时使用系统时钟。
	Clock clock.Clock
	// HTTPClient 是出站客户端；nil 时由驱动按 Timeout 创建。
	// 驱动传入的客户端必须关闭自动重试，避免与编排层重试叠加。
	HTTPClient *httpclient.Client
	// Aliyun 是阿里云驱动配置；仅 Driver=="aliyun" 时读取。
	Aliyun AliyunConfig
}

// AliyunConfig 是阿里云短信（Dysmsapi）配置。
//
// AccessKeyID/AccessKeySecret 只来自环境变量或 KMS，禁止写入 YAML 或镜像层。
type AliyunConfig struct {
	// Endpoint 默认 https://dysmsapi.aliyuncs.com。
	Endpoint string
	// Region 默认 cn-hangzhou。
	Region string
	// AccessKeyID 与 AccessKeySecret 用于 HMAC-SHA1 签名。
	AccessKeyID     string
	AccessKeySecret string
	// SignName 是默认短信签名；Message.SignName 可覆盖。
	SignName string
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
