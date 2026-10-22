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
	Redis           RedisConfig     `yaml:"redis"`
	Storage         StorageConfig   `yaml:"storage"`
	Worker          WorkerConfig    `yaml:"worker"`
	Tracing         TracingConfig   `yaml:"tracing"`
	RateLimit       RateLimitConfig `yaml:"rate_limit"`
	CORS            CORSConfig      `yaml:"cors"`
	IAM             IAMConfig       `yaml:"iam"`
}

// RedisConfig 是共享缓存 / 锁的 Redis 连接参数（可选）。
//
// Addr 为空表示本进程不启用 Redis；IAM `cache.driver=redis` 时必须配置。
// 口令只允许通过环境变量注入（SEC-01 / CFG-06）。
type RedisConfig struct {
	Addr      string `yaml:"addr"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	DB        int    `yaml:"db"`
	KeyPrefix string `yaml:"key_prefix"`
}

// Enabled 报告是否配置了 Redis。
func (r RedisConfig) Enabled() bool { return strings.TrimSpace(r.Addr) != "" }

// LogValue 实现 slog.LogValuer：口令只输出占位符（SEC-05）。
func (r RedisConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", r.Addr),
		slog.String("username", r.Username),
		slog.String("password", maskIfSet(r.Password)),
		slog.Int("db", r.DB),
		slog.String("key_prefix", r.KeyPrefix),
	)
}

// 鉴权数据来源模式（iam.mode，docs/rbac-iam.md §15.1）。
const (
	// IAMModeOff 使用装配期静态角色表（回滚位）。
	IAMModeOff = "off"
	// IAMModeShadow 计算快照但按静态表放行，仅记录差异。
	IAMModeShadow = "shadow"
	// IAMModeEnforce 以 IAM 快照为唯一真相。
	IAMModeEnforce = "enforce"
)

// 快照缓存驱动。
const (
	// IAMCacheDriverMemory 仅进程内 L1（local / dev）。
	IAMCacheDriverMemory = "memory"
	// IAMCacheDriverRedis L1 + Redis（staging / prod enforce 必须）。
	IAMCacheDriverRedis = "redis"
)

// IAMConfig 是 RBAC 权限管理（IAM）运行参数（docs/rbac-iam.md §12.1）。
//
// 默认 mode=off：未显式启用时保持历史静态权限表行为，避免升级即切换鉴权来源。
type IAMConfig struct {
	// Mode off | shadow | enforce。
	Mode string `yaml:"mode"`
	// LoadTimeout 回源 DB 超时上限；必须 ≤ 1s。
	LoadTimeout time.Duration `yaml:"load_timeout"`
	// Cache 两级快照缓存。
	Cache IAMCacheConfig `yaml:"cache"`
	// Limits 快照与角色规模上限，防止角色膨胀拖垮鉴权。
	Limits IAMLimitsConfig `yaml:"limits"`
	// Security 安全开关。
	Security IAMSecurityConfig `yaml:"security"`
	// Breakglass 破玻璃（限时超级权限）开关与允许主体。
	Breakglass IAMBreakglassConfig `yaml:"breakglass"`
}

// IAMCacheConfig 是快照缓存参数。
type IAMCacheConfig struct {
	// Driver memory | redis；prod + enforce 必须 redis。
	Driver string `yaml:"driver"`
	// L1TTL 进程内 TTL（撤销最坏生效窗口）；≤ 5s。
	L1TTL time.Duration `yaml:"l1_ttl"`
	// L2TTL Redis TTL；≤ 15m 且必须大于 L1TTL。
	L2TTL time.Duration `yaml:"l2_ttl"`
	// NegativeTTL 空快照 TTL，防异常主体打穿 DB。
	NegativeTTL time.Duration `yaml:"negative_ttl"`
	// KeyPrefix 缓存域段，默认 iam。
	KeyPrefix string `yaml:"key_prefix"`
	// L1MaxEntries 进程内最大条目数；≤0 时使用 10000。
	L1MaxEntries int `yaml:"l1_max_entries"`
}

// IAMLimitsConfig 是快照与角色规模上限。
type IAMLimitsConfig struct {
	// MaxRolesPerSubject 单主体最大生效角色数。
	MaxRolesPerSubject int `yaml:"max_roles_per_subject"`
	// MaxPermissionsPerRole 单角色最大权限点数。
	MaxPermissionsPerRole int `yaml:"max_permissions_per_role"`
	// MaxBindingsPerSubject 单主体最大绑定数。
	MaxBindingsPerSubject int `yaml:"max_bindings_per_subject"`
	// MaxCustomRolesPerMerchant B 端自定义角色数上限。
	MaxCustomRolesPerMerchant int `yaml:"max_custom_roles_per_merchant"`
}

// IAMBreakglassConfig 是破玻璃开关；生产默认关闭。
type IAMBreakglassConfig struct {
	// Enabled 是否启用破玻璃。
	Enabled bool `yaml:"enabled"`
	// AllowedSubjects 允许申请破玻璃的主体白名单；prod 启用时必填。
	AllowedSubjects []string `yaml:"allowed_subjects"`
}

// IAMSecurityConfig 是 IAM 安全开关。
type IAMSecurityConfig struct {
	// AllowWildcard 是否允许快照携带通配符；prod 强制 false。
	AllowWildcard bool `yaml:"allow_wildcard"`
	// RequireReason 高风险变更是否强制填写原因。
	RequireReason bool `yaml:"require_reason"`
}

// Enabled 报告 IAM 是否接管鉴权（shadow 与 enforce 都需要加载快照）。
func (c IAMConfig) Enabled() bool {
	return c.Mode == IAMModeShadow || c.Mode == IAMModeEnforce
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
		slog.Any("redis", c.Redis),
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
		slog.Group("iam",
			slog.String("mode", normalizeIAMMode(c.IAM.Mode)),
			slog.Duration("load_timeout", c.IAM.LoadTimeout),
			slog.Group("cache",
				slog.String("driver", c.IAM.Cache.Driver),
				slog.Duration("l1_ttl", c.IAM.Cache.L1TTL),
				slog.Duration("l2_ttl", c.IAM.Cache.L2TTL),
				slog.Duration("negative_ttl", c.IAM.Cache.NegativeTTL),
			),
			slog.Bool("allow_wildcard", c.IAM.Security.AllowWildcard),
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
	c.applyIAMDefaults()
}

// applyIAMDefaults 填充 IAM 默认值；Mode 保持空串，由 normalize 统一为 off。
func (c *Config) applyIAMDefaults() {
	if c.IAM.LoadTimeout == 0 {
		c.IAM.LoadTimeout = 300 * time.Millisecond
	}
	if c.IAM.Cache.Driver == "" {
		c.IAM.Cache.Driver = IAMCacheDriverMemory
	}
	if c.IAM.Cache.L1TTL == 0 {
		c.IAM.Cache.L1TTL = 3 * time.Second
	}
	if c.IAM.Cache.L2TTL == 0 {
		c.IAM.Cache.L2TTL = 5 * time.Minute
	}
	if c.IAM.Cache.NegativeTTL == 0 {
		c.IAM.Cache.NegativeTTL = 30 * time.Second
	}
	if c.IAM.Cache.KeyPrefix == "" {
		c.IAM.Cache.KeyPrefix = "iam"
	}
	if c.IAM.Cache.L1MaxEntries == 0 {
		c.IAM.Cache.L1MaxEntries = 10000
	}
	if c.IAM.Limits.MaxRolesPerSubject == 0 {
		c.IAM.Limits.MaxRolesPerSubject = 20
	}
	if c.IAM.Limits.MaxPermissionsPerRole == 0 {
		c.IAM.Limits.MaxPermissionsPerRole = 200
	}
	if c.IAM.Limits.MaxBindingsPerSubject == 0 {
		c.IAM.Limits.MaxBindingsPerSubject = 50
	}
	if c.IAM.Limits.MaxCustomRolesPerMerchant == 0 {
		c.IAM.Limits.MaxCustomRolesPerMerchant = 50
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
	c.IAM.Mode = normalizeIAMMode(c.IAM.Mode)
}

// normalizeIAMMode 归一化 iam.mode；空串视为 off（保持历史静态权限表行为）。
func normalizeIAMMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return IAMModeOff
	}
	return mode
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
	errs = append(errs, c.validateIAM()...)
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

// validateIAM 校验 IAM 模式、缓存 TTL、生产互斥项与端别约束（docs/rbac-iam.md §12.2）。
//
// 错误只包含字段路径与约束，不回显取值（CFG-05）。
func (c *Config) validateIAM() []error {
	// 完全未配置 iam 段时保持历史行为（zero-value Config 直接调用 Validate 也不会误报）；
	// 生产路径统一经 LoadConfig → applyDefaults，因此段内字段必然已填充。
	if c.IAM.Mode == "" && c.IAM.Cache.Driver == "" && c.IAM.LoadTimeout == 0 {
		return nil
	}
	var errs []error
	mode := normalizeIAMMode(c.IAM.Mode)
	switch mode {
	case IAMModeOff, IAMModeShadow, IAMModeEnforce:
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.mode: %s", c.IAM.Mode))
	}

	if c.Service != "" {
		// C 端与 worker 不挂载 IAM；显式开启会在无 transport 时静默失效，故启动即拒绝。
		if (c.Channel == "c" || c.Channel == "worker") && mode != IAMModeOff {
			errs = append(errs, fmt.Errorf("iam.mode must be off when channel is %s", c.Channel))
		}
	}

	if c.IAM.LoadTimeout <= 0 || c.IAM.LoadTimeout > time.Second {
		errs = append(errs, errors.New("iam.load_timeout must be in (0, 1s]"))
	}
	switch c.IAM.Cache.Driver {
	case IAMCacheDriverMemory:
	case IAMCacheDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when iam.cache.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.cache.driver: %s", c.IAM.Cache.Driver))
	}
	if c.IAM.Cache.L1TTL <= 0 || c.IAM.Cache.L1TTL > 5*time.Second {
		errs = append(errs, errors.New("iam.cache.l1_ttl must be in (0, 5s]"))
	}
	if c.IAM.Cache.L2TTL <= 0 || c.IAM.Cache.L2TTL > 15*time.Minute {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be in (0, 15m]"))
	}
	if c.IAM.Cache.L2TTL <= c.IAM.Cache.L1TTL {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be greater than iam.cache.l1_ttl"))
	}
	if c.IAM.Cache.NegativeTTL <= 0 || c.IAM.Cache.NegativeTTL > c.IAM.Cache.L2TTL {
		errs = append(errs, errors.New("iam.cache.negative_ttl must be in (0, iam.cache.l2_ttl]"))
	}
	if c.IAM.Cache.L1MaxEntries < 0 {
		errs = append(errs, errors.New("iam.cache.l1_max_entries must be >= 0"))
	}
	if c.IAM.Limits.MaxRolesPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_roles_per_subject must be > 0"))
	}
	if c.IAM.Limits.MaxPermissionsPerRole <= 0 {
		errs = append(errs, errors.New("iam.limits.max_permissions_per_role must be > 0"))
	}
	if c.IAM.Limits.MaxBindingsPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_bindings_per_subject must be > 0"))
	}
	if c.Channel == "biz" && c.IAM.Limits.MaxCustomRolesPerMerchant <= 0 {
		errs = append(errs, errors.New("iam.limits.max_custom_roles_per_merchant must be > 0 when channel=biz"))
	}

	if isProdLikeEnv(c.Env) && mode == IAMModeEnforce && c.IAM.Cache.Driver != IAMCacheDriverRedis {
		errs = append(errs, errors.New("iam.cache.driver must be redis when iam.mode=enforce in prod/staging"))
	}
	if isProdEnv(c.Env) && c.IAM.Security.AllowWildcard {
		errs = append(errs, errors.New("iam.security.allow_wildcard must be false when env=prod"))
	}
	if c.IAM.Breakglass.Enabled {
		if c.Channel != "admin" {
			errs = append(errs, errors.New("iam.breakglass.enabled is only supported on channel=admin"))
		}
		if mode != IAMModeEnforce {
			errs = append(errs, errors.New("iam.breakglass.enabled requires iam.mode=enforce"))
		}
		if isProdEnv(c.Env) && len(c.IAM.Breakglass.AllowedSubjects) == 0 {
			errs = append(errs, errors.New("iam.breakglass.allowed_subjects is required when env=prod"))
		}
		for i, subject := range c.IAM.Breakglass.AllowedSubjects {
			if strings.TrimSpace(subject) == "" {
				errs = append(errs, fmt.Errorf("iam.breakglass.allowed_subjects[%d] must not be empty", i))
			}
		}
	}
	return errs
}
