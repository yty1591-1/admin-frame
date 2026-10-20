package platform

import (
	"fmt"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/config"
)

// envPrefix 是本进程所有环境变量的统一前缀（CONFIG_STANDARDS CFG-04）。
const envPrefix = "ADMIN_FRAME"

// Config 进程配置根对象（按 cmd 对应 yaml 加载）。
//
// yaml 标签同时决定环境变量名：Env → ADMIN_FRAME_ENV、Log.Level →
// ADMIN_FRAME_LOG_LEVEL（pkg/config.BindEnv）。新增字段无需再改环境变量代码。
type Config struct {
	// Env 部署环境：local / dev / test / staging / prod（可空，表示未声明）。
	Env             string        `yaml:"env"`
	Service         string        `yaml:"service"`
	Channel         string        `yaml:"channel"`
	Addr            string        `yaml:"addr"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	Log             LogConfig     `yaml:"log"`
	Auth            AuthConfig    `yaml:"auth"`
	DB              DBConfig      `yaml:"db"`
	Storage         StorageConfig `yaml:"storage"`
	Worker          WorkerConfig  `yaml:"worker"`
	Tracing         TracingConfig `yaml:"tracing"`
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

// AuthConfig 鉴权相关配置（含演示模式）。
type AuthConfig struct {
	DemoMode        bool   `yaml:"demo_mode"`
	ExpectedChannel string `yaml:"expected_channel"`
}

// DBConfig 数据源驱动与连接串。
type DBConfig struct {
	Driver string `yaml:"driver"` // memory | mysql | postgres
	DSN    string `yaml:"dsn"`
}

// WorkerConfig 后台 worker 轮询等参数。
type WorkerConfig struct {
	PollInterval time.Duration `yaml:"poll_interval"`
}

// NewConfigLoader 构造进程配置加载器；供需要热更新（pkg/config.Watcher）的
// 长驻进程复用，普通入口直接用 LoadConfig 即可。
//
// 严格解析 YAML（未知字段报错），按 CFG-04 应用默认值、环境变量与校验。
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
}

// normalize 在环境变量覆盖后、Validate 前补齐跨字段派生值。
//
// auth.expected_channel 未显式声明时跟随 channel，保证环境变量改写
// channel 后两者仍然一致（CFG-04）。
func (c *Config) normalize() {
	if c.Auth.ExpectedChannel == "" {
		c.Auth.ExpectedChannel = c.Channel
	}
}

// Validate 校验必填项、枚举与生产互斥项（CFG-03/05）。
func (c *Config) Validate() error {
	if c.Service == "" {
		return fmt.Errorf("service is required")
	}
	if c.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	switch c.Channel {
	case "admin", "biz", "c", "worker":
	default:
		return fmt.Errorf("unsupported channel: %s", c.Channel)
	}
	if c.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	if c.Auth.ExpectedChannel != c.Channel {
		return fmt.Errorf("auth.expected_channel %q must equal channel %q", c.Auth.ExpectedChannel, c.Channel)
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("unsupported log.level: %s", c.Log.Level)
	}
	switch c.DB.Driver {
	case "memory", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported db.driver: %s", c.DB.Driver)
	}
	if c.DB.Driver != "memory" && c.DB.DSN == "" {
		return fmt.Errorf("db.dsn is required when driver is %s", c.DB.Driver)
	}
	if err := ValidateStorage(c.Env, c.Storage); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "", "local", "dev", "test", "staging", "prod":
	default:
		return fmt.Errorf("unsupported env: %s", c.Env)
	}
	if strings.EqualFold(c.Env, "prod") && c.Auth.DemoMode {
		return fmt.Errorf("auth.demo_mode must be false when env=prod")
	}
	switch strings.ToLower(strings.TrimSpace(c.Tracing.Exporter)) {
	case "", "none", "otlp", "stdout", "noop":
	default:
		return fmt.Errorf("unsupported tracing.exporter: %s", c.Tracing.Exporter)
	}
	if c.Tracing.IsEnabled() && strings.EqualFold(c.Tracing.Exporter, "otlp") && strings.TrimSpace(c.Tracing.Endpoint) == "" {
		return fmt.Errorf("tracing.endpoint is required when exporter=otlp")
	}
	if c.Tracing.SampleRatio != nil {
		r := *c.Tracing.SampleRatio
		if r < 0 || r > 1 {
			return fmt.Errorf("tracing.sample_ratio must be in [0,1]")
		}
	}
	if strings.EqualFold(c.Env, "prod") && strings.EqualFold(c.Tracing.Exporter, "stdout") {
		return fmt.Errorf("tracing.exporter=stdout is forbidden when env=prod")
	}
	return nil
}
