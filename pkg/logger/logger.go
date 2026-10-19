package logger

import (
	"log/slog"
	"strings"
)

// New 按 Config 创建带 service / channel / env 的 slog.Logger（OBS-01）。
func New(cfg Config) (*slog.Logger, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{
		Level:     parseLevel(cfg.Level),
		AddSource: cfg.AddSource,
	}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(cfg.Output, opts)
	} else {
		h = slog.NewJSONHandler(cfg.Output, opts)
	}
	if !cfg.DisableContext {
		h = NewContextHandler(h)
	}

	log := slog.New(h)
	attrs := make([]any, 0, 6)
	if cfg.Service != "" {
		attrs = append(attrs, FieldService, cfg.Service)
	}
	if cfg.Channel != "" {
		attrs = append(attrs, FieldChannel, cfg.Channel)
	}
	if cfg.Env != "" {
		attrs = append(attrs, FieldEnv, cfg.Env)
	}
	if len(attrs) > 0 {
		log = log.With(attrs...)
	}
	return log, nil
}

// Must 同 New，失败时 panic（仅进程启动装配）。
func Must(cfg Config) *slog.Logger {
	log, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return log
}
