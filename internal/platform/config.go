package platform

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 进程配置根对象（按 cmd 对应 yaml 加载）。
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

// LoadConfig 从 YAML 文件加载配置，应用默认值与环境变量覆盖后校验。
// 优先级：环境变量 > 配置文件 > 默认值（CONFIG_STANDARDS CFG-04）。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- config path is an operator CLI flag, not request input
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

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
	if c.Auth.ExpectedChannel == "" {
		c.Auth.ExpectedChannel = c.Channel
	}
	if c.Tracing.Exporter == "" {
		c.Tracing.Exporter = "none"
	}
}

// applyEnv 用 ADMIN_FRAME_* 环境变量覆盖文件配置（CFG-04）。
func (c *Config) applyEnv() {
	if v := os.Getenv("ADMIN_FRAME_ENV"); v != "" {
		c.Env = v
	}
	if v := os.Getenv("ADMIN_FRAME_SERVICE"); v != "" {
		c.Service = v
	}
	if v := os.Getenv("ADMIN_FRAME_CHANNEL"); v != "" {
		c.Channel = v
	}
	if v := os.Getenv("ADMIN_FRAME_ADDR"); v != "" {
		c.Addr = v
	}
	if v := os.Getenv("ADMIN_FRAME_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.ShutdownTimeout = d
		}
	}
	if v := os.Getenv("ADMIN_FRAME_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
	if v := os.Getenv("ADMIN_FRAME_LOG_FORMAT"); v != "" {
		c.Log.Format = v
	}
	if v := os.Getenv("ADMIN_FRAME_AUTH_DEMO_MODE"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			c.Auth.DemoMode = true
		case "0", "false", "no", "off":
			c.Auth.DemoMode = false
		}
	}
	if v := os.Getenv("ADMIN_FRAME_AUTH_EXPECTED_CHANNEL"); v != "" {
		c.Auth.ExpectedChannel = v
	}
	if v := os.Getenv("ADMIN_FRAME_DB_DRIVER"); v != "" {
		c.DB.Driver = v
	}
	if v := os.Getenv("ADMIN_FRAME_DB_DSN"); v != "" {
		c.DB.DSN = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_DRIVER"); v != "" {
		c.Storage.Driver = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_KEY_PREFIX"); v != "" {
		c.Storage.KeyPrefix = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_LOCAL_ROOT"); v != "" {
		c.Storage.Local.Root = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_S3_ENDPOINT"); v != "" {
		c.Storage.S3.Endpoint = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_S3_REGION"); v != "" {
		c.Storage.S3.Region = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_S3_BUCKET"); v != "" {
		c.Storage.S3.Bucket = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_ACCESS_KEY"); v != "" {
		c.Storage.S3.AccessKey = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_SECRET_KEY"); v != "" {
		c.Storage.S3.SecretKey = v
	}
	if v := os.Getenv("ADMIN_FRAME_STORAGE_S3_FORCE_PATH_STYLE"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			c.Storage.S3.ForcePathStyle = true
		case "0", "false", "no", "off":
			c.Storage.S3.ForcePathStyle = false
		}
	}
	if v := os.Getenv("ADMIN_FRAME_WORKER_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Worker.PollInterval = d
		}
	}
	if v := os.Getenv("ADMIN_FRAME_TRACING_ENABLED"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			on := true
			c.Tracing.Enabled = &on
		case "0", "false", "no", "off":
			off := false
			c.Tracing.Enabled = &off
		}
	}
	if v := os.Getenv("ADMIN_FRAME_TRACING_EXPORTER"); v != "" {
		c.Tracing.Exporter = v
	}
	if v := os.Getenv("ADMIN_FRAME_TRACING_ENDPOINT"); v != "" {
		c.Tracing.Endpoint = v
	}
	if v := os.Getenv("ADMIN_FRAME_TRACING_INSECURE"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			c.Tracing.Insecure = true
		case "0", "false", "no", "off":
			c.Tracing.Insecure = false
		}
	}
	if v := os.Getenv("ADMIN_FRAME_TRACING_SAMPLE_RATIO"); v != "" {
		var ratio float64
		if _, err := fmt.Sscanf(v, "%f", &ratio); err == nil {
			c.Tracing.SampleRatio = &ratio
		}
	}
	// channel 被环境变量改写后，若 expected 仍为空则对齐。
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
