package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/pkg/config"
)

// envPrefix 是本进程所有环境变量的统一前缀（CONFIG_STANDARDS CFG-04）。
const envPrefix = "ADMIN_FRAME"

// maskedValue 是日志中敏感字段的占位符（SECURITY_STANDARDS SEC-05）。
const maskedValue = "***"

// 鉴权模式（auth.mode）。
const (
	// AuthModeDemo 演示令牌；仅允许 local / dev / test。
	AuthModeDemo = "demo"
	// AuthModeJWT 使用 pkg/jwt 校验真实令牌。
	AuthModeJWT = "jwt"
)

// Config 进程配置根对象（按 cmd 对应 yaml 加载）。
//
// yaml 标签同时决定环境变量名：Env → ADMIN_FRAME_ENV、Log.Level →
// ADMIN_FRAME_LOG_LEVEL（pkg/config.BindEnv）。新增字段无需再改环境变量代码。
type Config struct {
	// Env 部署环境：local / dev / test / staging / prod（可空，表示未声明）。
	Env             string          `yaml:"env"`
	Service         string          `yaml:"service"`
	Channel         string          `yaml:"channel"`
	Addr            string          `yaml:"addr"`
	ShutdownTimeout time.Duration   `yaml:"shutdown_timeout"`
	HTTP            HTTPConfig      `yaml:"http"`
	Log             LogConfig       `yaml:"log"`
	Auth            AuthConfig      `yaml:"auth"`
	DB              DBConfig        `yaml:"db"`
	Storage         StorageConfig   `yaml:"storage"`
	Worker          WorkerConfig    `yaml:"worker"`
	Tracing         TracingConfig   `yaml:"tracing"`
	RateLimit       RateLimitConfig `yaml:"rate_limit"`
	CORS            CORSConfig      `yaml:"cors"`
}

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
}

// RateLimitConfig 入口限流（REL-05）；按调用方维度分桶（见 middleware.DefaultRateLimitKey）。
type RateLimitConfig struct {
	// Enabled 是否启用；默认关闭，按端开启。
	Enabled bool `yaml:"enabled"`
	// RPS 每秒补充令牌数；须 >0。
	RPS float64 `yaml:"rps"`
	// Burst 桶容量；须 ≥1。
	Burst int `yaml:"burst"`
	// IdleTTL 空闲桶回收阈值；0 时使用 10m。
	IdleTTL time.Duration `yaml:"idle_ttl"`
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

// effectiveMode 返回生效的鉴权模式。
//
// 未显式声明 mode 时的回退规则：
//   - prod / staging → jwt（缺少 JWT 配置会直接启动失败，绝不静默回退 demo）；
//   - 其余环境（local/dev/test/未声明）→ demo，便于本地起服务。
func (a AuthConfig) effectiveMode(env string) string {
	if mode := strings.ToLower(strings.TrimSpace(a.Mode)); mode != "" {
		return mode
	}
	if isProdLikeEnv(env) {
		return AuthModeJWT
	}
	return AuthModeDemo
}

// DBConfig 数据源驱动与连接串。
type DBConfig struct {
	Driver string `yaml:"driver"` // memory | mysql | postgres
	DSN    string `yaml:"dsn"`
}

// LogValue 实现 slog.LogValuer：DSN 只保留 scheme，避免凭据进日志（SEC-05）。
func (c DBConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("driver", c.Driver),
		slog.String("dsn", redactDSN(c.DSN)),
	)
}

// WorkerConfig 后台 worker 轮询等参数。
type WorkerConfig struct {
	PollInterval time.Duration `yaml:"poll_interval"`
}

// LogValue 实现 slog.LogValuer：输出可安全落盘的配置快照（SEC-05）。
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("service", c.Service),
		slog.String("channel", c.Channel),
		slog.String("addr", c.Addr),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
		slog.Group("http",
			slog.Duration("read_timeout", c.HTTP.ReadTimeout),
			slog.Duration("write_timeout", c.HTTP.WriteTimeout),
			slog.Duration("idle_timeout", c.HTTP.IdleTimeout),
			slog.Duration("timeout", c.HTTP.Timeout),
		),
		slog.Group("log",
			slog.String("level", c.Log.Level),
			slog.String("format", c.Log.Format),
		),
		slog.Group("auth",
			slog.String("mode", c.Auth.effectiveMode(c.Env)),
			slog.String("expected_channel", c.Auth.ExpectedChannel),
			slog.Group("jwt",
				slog.String("alg", c.Auth.JWT.Alg),
				slog.String("issuer", c.Auth.JWT.Issuer),
				slog.String("secret", maskIfSet(c.Auth.JWT.Secret)),
				slog.Int("rotating_secrets", len(c.Auth.JWT.Secrets)),
			),
		),
		slog.Any("db", c.DB),
		slog.Any("storage", c.Storage),
		slog.Group("worker", slog.Duration("poll_interval", c.Worker.PollInterval)),
		slog.Group("tracing",
			slog.String("exporter", c.Tracing.Exporter),
			slog.Bool("enabled", c.Tracing.IsEnabled()),
		),
		slog.Group("rate_limit",
			slog.Bool("enabled", c.RateLimit.Enabled),
			slog.Float64("rps", c.RateLimit.RPS),
			slog.Int("burst", c.RateLimit.Burst),
		),
	)
}

// maskIfSet 在取值非空时返回占位符，避免泄露“密钥是否存在”以外的信息。
func maskIfSet(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return maskedValue
}

// redactDSN 只保留 DSN 的 scheme 部分，其余替换为占位符。
func redactDSN(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return ""
	}
	if idx := strings.Index(s, "://"); idx >= 0 {
		return s[:idx+3] + maskedValue
	}
	return maskedValue
}

// isProdEnv 报告环境是否为生产。
func isProdEnv(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), "prod")
}

// isProdLikeEnv 报告环境是否按生产标准校验（prod / staging）。
func isProdLikeEnv(env string) bool {
	return isProdEnv(env) || strings.EqualFold(strings.TrimSpace(env), "staging")
}

// NewConfigLoader 构造进程配置加载器。
//
// 当前所有进程只在启动期加载一次（LoadConfig）；pkg/config.Watcher 的热更新
// 能力为预留项，尚未接入任何 cmd。启用前须先明确可热更新字段白名单并写 ADR
// （CONFIG_STANDARDS §9）。保持导出是为了让未来的长驻进程复用同一套校验。
func NewConfigLoader(path string) (*config.Loader[Config], error) {
	return config.NewLoader(config.Options[Config]{
		Files:     []string{path},
		EnvPrefix: envPrefix,
		Defaults:  (*Config).applyDefaults,
		Normalize: (*Config).normalize,
		Validate:  (*Config).Validate,
	})
}

// LoadConfig 从 YAML 文件加载配置，应用默认值与环境变量覆盖后校验。
//
// 优先级：环境变量 > 配置文件 > 默认值（CONFIG_STANDARDS CFG-04）。
// 环境变量无法解析（如非法 duration/bool）时返回错误，不再静默忽略；
// 错误只包含字段路径与变量名，不含取值（SEC-05）。
func LoadConfig(path string) (*Config, error) {
	loader, err := NewConfigLoader(path)
	if err != nil {
		return nil, err
	}
	return loader.Load()
}

// dbOnlyConfig 是 LoadDBConfig 的解码目标，只声明 db 段。
type dbOnlyConfig struct {
	DB DBConfig `yaml:"db"`
}

// LoadDBConfig 仅加载配置文件的 db 段，并应用 ADMIN_FRAME_DB_* 环境变量覆盖。
//
// 供 cmd/migrate 等只需要数据库连接的工具复用 CFG-04 的配置优先级；
// 不校验服务级字段（service/channel/addr）。path 为空时只读取环境变量。
// 传入完整服务配置时其余字段被忽略（AllowUnknownFields），便于直接复用线上 yaml。
func LoadDBConfig(path string) (DBConfig, error) {
	opts := config.Options[dbOnlyConfig]{
		EnvPrefix:          envPrefix,
		AllowUnknownFields: true,
	}
	if path != "" {
		opts.Files = []string{path}
	}
	loader, err := config.NewLoader(opts)
	if err != nil {
		return DBConfig{}, err
	}
	cfg, err := loader.Load()
	if err != nil {
		return DBConfig{}, err
	}
	return cfg.DB, nil
}

// applyDefaults 在解码文件前填充默认值。
//
// 只处理与其它字段无关的常量默认值；跨字段派生放在 normalize，
// 以免被文件或环境变量覆盖后失去一致性。
func (c *Config) applyDefaults() {
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 15 * time.Second
	}
	if c.HTTP.ReadHeaderTimeout == 0 {
		c.HTTP.ReadHeaderTimeout = 5 * time.Second
	}
	if c.HTTP.ReadTimeout == 0 {
		c.HTTP.ReadTimeout = 15 * time.Second
	}
	if c.HTTP.WriteTimeout == 0 {
		c.HTTP.WriteTimeout = 30 * time.Second
	}
	if c.HTTP.IdleTimeout == 0 {
		c.HTTP.IdleTimeout = 60 * time.Second
	}
	if c.HTTP.MaxHeaderBytes == 0 {
		c.HTTP.MaxHeaderBytes = 1 << 20
	}
	if c.HTTP.Timeout == 0 {
		c.HTTP.Timeout = 30 * time.Second
	}
	if c.HTTP.BodyLimitBytes == 0 {
		c.HTTP.BodyLimitBytes = 32 << 20
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	if c.DB.Driver == "" {
		c.DB.Driver = "memory"
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "memory"
	}
	if c.Worker.PollInterval == 0 {
		c.Worker.PollInterval = 5 * time.Second
	}
	if c.Tracing.Exporter == "" {
		c.Tracing.Exporter = "none"
	}
	if c.RateLimit.IdleTTL == 0 {
		c.RateLimit.IdleTTL = 10 * time.Minute
	}
}

// normalize 在环境变量覆盖后、Validate 前补齐跨字段派生值。
//
// auth.expected_channel 未显式声明时跟随 channel，保证环境变量改写
// channel 后两者仍然一致；auth.mode 未声明时按环境回退（CFG-04）。
func (c *Config) normalize() {
	if c.Auth.ExpectedChannel == "" {
		c.Auth.ExpectedChannel = c.Channel
	}
	c.Auth.Mode = c.Auth.effectiveMode(c.Env)
}

// Validate 校验必填项、枚举与生产互斥项（CFG-03/05）。
//
// 错误通过 errors.Join 聚合，启动日志可一次列出全部问题；
// 错误文本只包含字段路径，不回显取值。
func (c *Config) Validate() error {
	var errs []error
	if c.Service == "" {
		errs = append(errs, errors.New("service is required"))
	}
	if c.Channel == "" {
		errs = append(errs, errors.New("channel is required"))
	} else {
		switch c.Channel {
		case "admin", "biz", "c", "worker":
		default:
			errs = append(errs, fmt.Errorf("unsupported channel: %s", c.Channel))
		}
	}
	if c.Addr == "" {
		errs = append(errs, errors.New("addr is required"))
	}
	if c.Auth.ExpectedChannel != c.Channel {
		errs = append(errs, fmt.Errorf("auth.expected_channel %q must equal channel %q", c.Auth.ExpectedChannel, c.Channel))
	}
	errs = append(errs, c.validateLog()...)
	errs = append(errs, c.validateHTTP()...)
	errs = append(errs, c.validateAuth()...)
	errs = append(errs, c.validateDB()...)
	if err := ValidateStorage(c.Env, c.Storage); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.validateEnv()...)
	errs = append(errs, c.validateTracing()...)
	errs = append(errs, c.validateRateLimit()...)
	return errors.Join(errs...)
}

// validateLog 校验日志级别与格式；prod 禁止 text（OBS-01）。
func (c *Config) validateLog() []error {
	var errs []error
	switch strings.ToLower(strings.TrimSpace(c.Log.Level)) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("unsupported log.level: %s", c.Log.Level))
	}
	switch strings.ToLower(strings.TrimSpace(c.Log.Format)) {
	case "json", "text":
	default:
		// 必须在启动前拦截：NewLogger 使用 logger.Must，非法格式会 panic。
		errs = append(errs, fmt.Errorf("unsupported log.format: %s", c.Log.Format))
	}
	if isProdEnv(c.Env) && strings.EqualFold(strings.TrimSpace(c.Log.Format), "text") {
		errs = append(errs, errors.New("log.format=text is forbidden when env=prod"))
	}
	return errs
}

// validateHTTP 校验 Server 加固参数与请求链路参数（REL-01/02）。
func (c *Config) validateHTTP() []error {
	var errs []error
	if c.HTTP.ReadHeaderTimeout < 0 {
		errs = append(errs, errors.New("http.read_header_timeout must be >= 0"))
	}
	if c.HTTP.MaxHeaderBytes < 0 {
		errs = append(errs, errors.New("http.max_header_bytes must be >= 0"))
	}
	if c.HTTP.Timeout < 0 {
		errs = append(errs, errors.New("http.timeout must be >= 0"))
	}
	if isProdEnv(c.Env) && c.HTTP.Timeout <= 0 {
		errs = append(errs, errors.New("http.timeout must be > 0 when env=prod"))
	}
	if c.HTTP.BodyLimitBytes < 0 {
		errs = append(errs, errors.New("http.body_limit_bytes must be >= 0"))
	}
	return errs
}

// validateAuth 校验鉴权模式、端别隔离与 JWT 配置（SEC-04 / CFG-05）。
func (c *Config) validateAuth() []error {
	var errs []error
	mode := c.Auth.effectiveMode(c.Env)
	switch mode {
	case AuthModeDemo, AuthModeJWT:
	default:
		errs = append(errs, fmt.Errorf("unsupported auth.mode: %s", c.Auth.Mode))
	}
	if mode == AuthModeDemo && isProdEnv(c.Env) {
		errs = append(errs, errors.New("auth.mode=demo is forbidden when env=prod"))
	}
	if mode == AuthModeJWT {
		if err := c.Auth.JWT.Validate(c.Channel); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// validateDB 校验驱动枚举与 DSN 必填。
func (c *Config) validateDB() []error {
	var errs []error
	switch c.DB.Driver {
	case "memory", "mysql", "postgres":
	default:
		errs = append(errs, fmt.Errorf("unsupported db.driver: %s", c.DB.Driver))
		return errs
	}
	if c.DB.Driver != "memory" && c.DB.DSN == "" {
		errs = append(errs, fmt.Errorf("db.dsn is required when driver is %s", c.DB.Driver))
	}
	return errs
}

// validateEnv 校验环境枚举与生产互斥项。
func (c *Config) validateEnv() []error {
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "", "local", "dev", "test", "staging", "prod":
	default:
		return []error{fmt.Errorf("unsupported env: %s", c.Env)}
	}
	return nil
}

// validateTracing 校验 exporter、endpoint 与生产互斥项（OBS）。
func (c *Config) validateTracing() []error {
	var errs []error
	switch strings.ToLower(strings.TrimSpace(c.Tracing.Exporter)) {
	case "", "none", "otlp", "stdout", "noop":
	default:
		errs = append(errs, fmt.Errorf("unsupported tracing.exporter: %s", c.Tracing.Exporter))
	}
	if c.Tracing.IsEnabled() && strings.EqualFold(c.Tracing.Exporter, "otlp") && strings.TrimSpace(c.Tracing.Endpoint) == "" {
		errs = append(errs, errors.New("tracing.endpoint is required when exporter=otlp"))
	}
	if c.Tracing.SampleRatio != nil {
		if r := *c.Tracing.SampleRatio; r < 0 || r > 1 {
			errs = append(errs, errors.New("tracing.sample_ratio must be in [0,1]"))
		}
	}
	if isProdEnv(c.Env) && strings.EqualFold(c.Tracing.Exporter, "stdout") {
		errs = append(errs, errors.New("tracing.exporter=stdout is forbidden when env=prod"))
	}
	return errs
}

// validateRateLimit 校验限流参数（REL-05）。
func (c *Config) validateRateLimit() []error {
	if !c.RateLimit.Enabled {
		return nil
	}
	var errs []error
	if c.RateLimit.RPS <= 0 {
		errs = append(errs, errors.New("rate_limit.rps must be > 0 when rate_limit.enabled=true"))
	}
	if c.RateLimit.Burst < 1 {
		errs = append(errs, errors.New("rate_limit.burst must be >= 1 when rate_limit.enabled=true"))
	}
	if c.RateLimit.IdleTTL < 0 {
		errs = append(errs, errors.New("rate_limit.idle_ttl must be >= 0"))
	}
	return errs
}
