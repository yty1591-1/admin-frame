package platform

import (
	"log/slog"

	"github.com/admin-frame/admin-frame/pkg/logger"
)

// NewLogger 按配置创建带 service / channel / env 字段的 slog.Logger（OBS-01）。
// 实现下沉至 pkg/logger；本函数仅做进程配置映射。
func NewLogger(cfg *Config) *slog.Logger {
	return logger.Must(logger.Config{
		Level:   cfg.Log.Level,
		Format:  cfg.Log.Format,
		Service: cfg.Service,
		Channel: cfg.Channel,
		Env:     cfg.Env,
	})
}
