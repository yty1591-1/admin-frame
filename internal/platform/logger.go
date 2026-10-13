package platform

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger 按配置创建带 service / channel / env 字段的 slog.Logger（OBS-01）。
func NewLogger(cfg *Config) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.EqualFold(cfg.Log.Format, "text") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler).With(
		"service", cfg.Service,
		"channel", cfg.Channel,
		"env", cfg.Env,
	)
}
