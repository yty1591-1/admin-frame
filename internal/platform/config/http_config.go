package config

import (
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
)

// HTTPConfig 入口 HTTP Server 与请求链路的加固参数（REL-02 / REL-01）。
type HTTPConfig struct {
	// ReadHeaderTimeout 读取请求头的上限；防御 slowloris，固定建议 5s。
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	// ReadTimeout 读取整个请求（含 body）的上限；0 表示不限制（不推荐）。
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 写响应的上限；0 表示不限制（大文件/流式接口须显式说明）。
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// IdleTimeout keep-alive 空闲连接上限。
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// MaxHeaderBytes 请求头总字节上限；0 表示使用标准库默认值。
	MaxHeaderBytes int `yaml:"max_header_bytes"`
	// Timeout 单请求 context deadline（软超时，中间件注入）；0 表示不注入。
	Timeout time.Duration `yaml:"timeout"`
	// BodyLimitBytes 请求体上限；≤0 表示不限制（文件上传接口须显式放大）。
	BodyLimitBytes int64 `yaml:"body_limit_bytes"`
	// TrustedProxies 可信代理 CIDR 列表；只有来自这些地址的请求才解析 X-Forwarded-For。
	// 为空表示不信任任何代理头，直接使用 RemoteAddr（IDN-P1-04）。
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// RateLimitConfig 入口限流（REL-05）；按调用方维度分桶（见 middleware.DefaultRateLimitKey）。
type RateLimitConfig struct {
	// Enabled 是否启用；默认关闭，按端开启。
	Enabled bool `yaml:"enabled"`
	// Driver memory | redis；prod/staging 启用时必须 redis（否则配额被副本数放大）。
	Driver string `yaml:"driver"`
	// Key 分键策略：channel | channel_user | channel_tenant_user | client。
	Key string `yaml:"key"`
	// FailureMode Redis 故障策略：fail_open（默认）| fail_closed。
	FailureMode string `yaml:"failure_mode"`
	// RPS 每秒补充令牌数；须 >0。
	RPS float64 `yaml:"rps"`
	// Burst 桶容量；须 ≥1。
	Burst int `yaml:"burst"`
	// IdleTTL 空闲桶回收阈值；0 时使用 10m。
	IdleTTL time.Duration `yaml:"idle_ttl"`
	// DegradeOnFailure fail_closed 下 Redis 连续故障后是否进入宽松降级窗口；
	// 降级放行会带 Degraded 标记，供告警而不是静默失效（P1-4）。
	DegradeOnFailure bool `yaml:"degrade_on_failure"`
}

// CORSConfig 跨域配置；生产须显式 allow_origins，禁止反射任意 Origin。
type CORSConfig struct {
	// AllowOrigins 允许的 Origin；为空表示不启用 CORS 中间件。
	AllowOrigins []string `yaml:"allow_origins"`
	// AllowHeaders 允许的请求头；空时使用 pkg/middleware 默认值。
	AllowHeaders []string `yaml:"allow_headers"`
	// ExposeHeaders 暴露给浏览器的响应头。
	ExposeHeaders []string `yaml:"expose_headers"`
	// AllowCredentials 是否允许 Cookie；与 "*" 互斥。
	AllowCredentials bool `yaml:"allow_credentials"`
	// MaxAge 预检缓存时长；≤0 不写该响应头。
	MaxAge time.Duration `yaml:"max_age"`
}

// TracingConfig OpenTelemetry 追踪（OBS）；装配见 pkg/tracing。
type TracingConfig struct {
	// Enabled 为 false 时使用 noop（不生成有效 TraceID）；nil 表示默认 true。
	Enabled *bool `yaml:"enabled"`
	// Exporter：none（默认，只生成 ID）/ otlp / stdout / noop。
	Exporter string `yaml:"exporter"`
	// Endpoint OTLP/HTTP 地址（exporter=otlp 时必填），如 localhost:4318。
	Endpoint string `yaml:"endpoint"`
	// SampleRatio 头采样 [0,1]；nil 表示 1.0。
	SampleRatio *float64 `yaml:"sample_ratio"`
	// Insecure OTLP 明文（无 TLS）；仅限本地/内网。
	Insecure bool `yaml:"insecure"`
}

// IsEnabled 报告追踪是否启用；未配置时默认 true。
func (t TracingConfig) IsEnabled() bool {
	if t.Enabled == nil {
		return true
	}
	return *t.Enabled
}

// LogConfig 日志级别与格式。
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// AuthConfig 鉴权相关配置（含演示模式与 JWT）。
type AuthConfig struct {
	// Mode 鉴权模式：demo 或 jwt；未声明时按环境回退（见 effectiveMode）。
	Mode string `yaml:"mode"`
	// ExpectedChannel 期望端别；须与进程 channel 一致（缺省时由 normalize 派生）。
	ExpectedChannel string `yaml:"expected_channel"`
	// JWT 真实令牌校验参数；mode=jwt 时生效。
	JWT security.JWTConfig `yaml:"jwt"`
}

// EffectiveMode 返回生效的鉴权模式。
//
// 未显式声明 mode 时的回退规则：
//   - prod / staging → jwt（缺少 JWT 配置会直接启动失败，绝不静默回退 demo）；
//   - 其余环境（local/dev/test/未声明）→ demo，便于本地起服务。
//
// EffectiveMode 返回解析后的鉴权模式，供平台装配层使用。
func (a AuthConfig) EffectiveMode(env string) string { return a.effectiveMode(env) }

func (a AuthConfig) effectiveMode(env string) string {
	if mode := strings.ToLower(strings.TrimSpace(a.Mode)); mode != "" {
		return mode
	}
	if isProdLikeEnv(env) {
		return AuthModeJWT
	}
	return AuthModeDemo
}
