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
	Worker          WorkerConfig  `yaml:"worker"`
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
	if c.Worker.PollInterval == 0 {
		c.Worker.PollInterval = 5 * time.Second
	}
	if c.Auth.ExpectedChannel == "" {
		c.Auth.ExpectedChannel = c.Channel
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
	if v := os.Getenv("ADMIN_FRAME_WORKER_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Worker.PollInterval = d
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
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "", "local", "dev", "test", "staging", "prod":
	default:
		return fmt.Errorf("unsupported env: %s", c.Env)
	}
	if strings.EqualFold(c.Env, "prod") && c.Auth.DemoMode {
		return fmt.Errorf("auth.demo_mode must be false when env=prod")
	}
	return nil
}
