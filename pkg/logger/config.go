package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config 日志装配参数。
type Config struct {
	// Level：debug / info / warn / error；默认 info。
	Level string
	// Format：json（默认）或 text（仅 local/dev）。
	Format string
	// Output 输出目标；nil 表示 os.Stdout。
	Output io.Writer

	// Service 进程名，如 admin-api（OBS-01 必填建议）。
	Service string
	// Channel 端标识：admin / biz / c / worker。
	Channel string
	// Env 部署环境：local / dev / test / staging / prod。
	Env string

	// AddSource 为 true 时记录源码位置（开发排障；生产慎用）。
	AddSource bool
	// DisableContext 为 true 时不包装 ContextHandler（测试用）。
	DisableContext bool
}

// Validate 校验可选硬约束（空 service 仅警告式允许，便于单测）。
func (c Config) Validate() error {
	switch strings.ToLower(strings.TrimSpace(c.Format)) {
	case "", "json", "text":
	default:
		return fmt.Errorf("logger: unsupported format %q", c.Format)
	}
	switch strings.ToLower(strings.TrimSpace(c.Level)) {
	case "", "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("logger: unsupported level %q", c.Level)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Output == nil {
		c.Output = os.Stdout
	}
	if strings.TrimSpace(c.Level) == "" {
		c.Level = "info"
	}
	if strings.TrimSpace(c.Format) == "" {
		c.Format = "json"
	}
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
