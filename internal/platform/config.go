package platform

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 进程配置根对象（按 cmd 对应 yaml 加载）。
type Config struct {
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

// LoadConfig 从 YAML 文件加载配置，应用默认值并校验。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
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

// Validate 校验必填项与受支持的 DB 驱动。
func (c *Config) Validate() error {
	if c.Service == "" {
		return fmt.Errorf("service is required")
	}
	if c.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	if c.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	switch c.DB.Driver {
	case "memory", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported db.driver: %s", c.DB.Driver)
	}
	if c.DB.Driver != "memory" && c.DB.DSN == "" {
		return fmt.Errorf("db.dsn is required when driver is %s", c.DB.Driver)
	}
	return nil
}
