package observability

import (
	"log/slog"

	"github.com/admin-frame/admin-frame/internal/platform/config"

	"github.com/admin-frame/admin-frame/pkg/logger"
)

// NewLogger 按配置创建带 service / channel / env 字段的 slog.Logger（OBS-01）。
// 实现下沉至 pkg/logger；本函数仅做进程配置映射。
func NewLogger(cfg *config.Config) *slog.Logger {
	log, _ := NewLoggerWithLevel(cfg)
	return log
}

// NewLoggerWithLevel 与 NewLogger 相同，但额外返回运行期级别开关。
//
// 调用方把 *slog.LevelVar 交给配置中心热更通道后，`log.level` 发布即可生效
// （ADR-0015 白名单第 1 项），无需重建 Handler。
func NewLoggerWithLevel(cfg *config.Config) (*slog.Logger, *slog.LevelVar) {
	level := &slog.LevelVar{}
	log := logger.Must(logger.Config{
		Level:    cfg.Log.Level,
		Format:   cfg.Log.Format,
		Service:  cfg.Service,
		Channel:  cfg.Channel,
		Env:      cfg.Env,
		LevelVar: level,
	})
	return log, level
}
