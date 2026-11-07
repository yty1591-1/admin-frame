package httpclient

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/admin-frame/admin-frame/pkg/retry"
)

const (
	defaultTimeout               = 10 * time.Second
	defaultMaxIdleConns          = 100
	defaultMaxIdleConnsPerHost   = 10
	defaultIdleConnTimeout       = 90 * time.Second
	defaultTLSHandshakeTimeout   = 5 * time.Second
	defaultResponseHeaderTimeout = 10 * time.Second
	defaultUserAgent             = "admin-frame-httpclient/1.0"
)

// Config 出站 HTTP 客户端配置。
type Config struct {
	// Timeout 单次请求客户端超时（含连接与读 body 前）；须 >0，默认 10s。
	// 仍须在调用方 context 设更短的下游 deadline（REL 超时分层）。
	Timeout time.Duration

	// Transport 底层 Transport；nil 时使用安全默认值。
	Transport http.RoundTripper
	// MaxIdleConns 全局空闲连接；仅当 Transport 为 nil 时生效。
	MaxIdleConns int
	// MaxIdleConnsPerHost 每主机空闲连接。
	MaxIdleConnsPerHost int
	// IdleConnTimeout 空闲连接超时。
	IdleConnTimeout time.Duration
	// TLSHandshakeTimeout TLS 握手超时。
	TLSHandshakeTimeout time.Duration
	// ResponseHeaderTimeout 等待响应头超时。
	ResponseHeaderTimeout time.Duration
	// DialTimeout 建连超时。
	DialTimeout time.Duration
	// KeepAlive TCP keepalive。
	KeepAlive time.Duration

	// UserAgent 默认 UA；空则使用包默认；已有 Header 不覆盖。
	UserAgent string

	// Retry 重试策略；零值表示使用 retry.DefaultPolicy。
	Retry retry.Policy
	// DisableRetry 关闭重试。
	DisableRetry bool
	// RetryNonIdempotent 允许对 POST/PATCH/DELETE 等非幂等方法重试（默认 false，危险）。
	RetryNonIdempotent bool
	// RetryStatusCodes 额外视为可重试的状态码；nil 时用默认集合。
	RetryStatusCodes []int

	// Observer 每次尝试结果的观测回调；nil 时不做记录。
	Observer AttemptObserver

	// Tracer 出站 CLIENT span；nil 时 otel.Tracer("http.client")。
	Tracer trace.Tracer
	// Propagator 上下文传播；nil 时用全局。
	Propagator propagation.TextMapPropagator
	// DisableTracing 关闭 span 与头注入。
	DisableTracing bool
	// InjectRequestID 为 true（默认）且请求无 X-Request-ID 时，用 TraceID 回填。
	InjectRequestID *bool
}

func (c *Config) applyDefaults() {
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = defaultMaxIdleConns
	}
	if c.MaxIdleConnsPerHost <= 0 {
		c.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}
	if c.IdleConnTimeout <= 0 {
		c.IdleConnTimeout = defaultIdleConnTimeout
	}
	if c.TLSHandshakeTimeout <= 0 {
		c.TLSHandshakeTimeout = defaultTLSHandshakeTimeout
	}
	if c.ResponseHeaderTimeout <= 0 {
		c.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.KeepAlive <= 0 {
		c.KeepAlive = 30 * time.Second
	}
	if c.UserAgent == "" {
		c.UserAgent = defaultUserAgent
	}
	if c.InjectRequestID == nil {
		v := true
		c.InjectRequestID = &v
	}
	if !c.DisableRetry {
		c.Retry = c.Retry.Normalize()
	}
	if c.Tracer == nil {
		c.Tracer = otel.Tracer("http.client")
	}
	if c.Propagator == nil {
		c.Propagator = otel.GetTextMapPropagator()
	}
	if c.RetryStatusCodes == nil {
		c.RetryStatusCodes = append([]int(nil), defaultRetryStatuses...)
	}
}

// Validate 校验配置。
func (c Config) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("%w: Timeout must be > 0", ErrInvalidConfig)
	}
	return nil
}

// TransportOrDefault 返回显式 Transport 或按本配置构建的安全默认 Transport。
//
// 供需要在 Transport 链上叠加装饰器（如熔断、指标）的调用方复用同一套
// 连接池与超时默认值，避免绕过本包直接使用 http.DefaultTransport。
func (c Config) TransportOrDefault() http.RoundTripper {
	c.applyDefaults()
	return c.buildTransport()
}

func (c Config) buildTransport() http.RoundTripper {
	if c.Transport != nil {
		return c.Transport
	}
	dialer := &net.Dialer{
		Timeout:   c.DialTimeout,
		KeepAlive: c.KeepAlive,
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          c.MaxIdleConns,
		MaxIdleConnsPerHost:   c.MaxIdleConnsPerHost,
		IdleConnTimeout:       c.IdleConnTimeout,
		TLSHandshakeTimeout:   c.TLSHandshakeTimeout,
		ResponseHeaderTimeout: c.ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
