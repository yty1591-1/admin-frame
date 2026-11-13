package snapshotconfig

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/pkg/config"
	"github.com/admin-frame/admin-frame/pkg/configclient"
	pkgmetrics "github.com/admin-frame/admin-frame/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

// DefaultDualReadInterval 是运行期双读的默认周期。
//
// 双读只做迁移期取证，不需要跟随 5s 快照轮询；30s 足以支撑「7 天零差异」
// 的采样密度，同时避免无意义的重复扫描。
const DefaultDualReadInterval = 30 * time.Second

// SnapshotReader 是周期双读所需的最小读取能力，由 *configclient.Client 实现。
type SnapshotReader interface {
	// Namespaces 返回当前启用的 namespace。
	Namespaces() []string
	// SnapshotFor 返回指定 namespace 的当前快照；不存在时 ok=false。
	SnapshotFor(namespace string) (configclient.Snapshot, bool)
}

// PeriodicConfig 是运行期双读的装配参数。
type PeriodicConfig struct {
	// ConfigPath 是文件基线路径（迁移期唯一取值基线）；必填。
	ConfigPath string
	// Env 是快照归属环境；必填。
	Env string
	// Reader 是当前快照来源；nil 表示未接入配置中心，周期对比不启动。
	Reader SnapshotReader
	// Logger 可为 nil，退化为 slog.Default()。
	Logger *slog.Logger
	// Metrics 可为 nil，此时只对比与记日志，不打点。
	Metrics *pkgmetrics.Metrics
	// Interval 是采样周期；<=0 取 DefaultDualReadInterval。
	Interval time.Duration
}

// StartPeriodic 用平台的后台任务注册器启动周期双读；不阻塞启动流程。
//
// register 为 nil（无后台任务能力）时为空操作，与 StartRefresh 的语义一致。
func StartPeriodic(register func(task func(context.Context) error), cfg PeriodicConfig) {
	if register == nil {
		return
	}
	register(func(ctx context.Context) error {
		return RunPeriodic(ctx, cfg)
	})
}

// RunPeriodic 周期性对比文件基线与当前签名快照，直到 ctx 取消。
//
// 与启动期一次性对比共用同一组指标与日志去重逻辑，因此可以反复采样而不刷屏。
// 基线文件被删除（无文件启动演练）时打一条日志后正常退出，不视为错误。
func RunPeriodic(ctx context.Context, cfg PeriodicConfig) error {
	if cfg.Reader == nil || strings.TrimSpace(cfg.ConfigPath) == "" {
		return nil
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var registerer prometheus.Registerer
	if cfg.Metrics != nil {
		registerer = cfg.Metrics.Registerer()
	}
	reporter, err := NewReporter(cfg.ConfigPath, cfg.Env, logger, registerer)
	if err != nil {
		return err
	}
	// 先确认基线存在：无文件时应直接退出，而不是每个周期都报一次缺失。
	if _, err := reporter.flat(); err != nil {
		if errors.Is(err, platformconfig.ErrMissingFile) {
			logger.Info("config dual-read periodic skipped: baseline file is absent",
				"path", cfg.ConfigPath)
			return nil
		}
		return err
	}

	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultDualReadInterval
	}
	logger.Info("config dual-read periodic enabled",
		"path", cfg.ConfigPath, "interval", interval,
		"namespaces", strings.Join(cfg.Reader.Namespaces(), ","))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("config dual-read periodic stopped")
			return nil
		case <-ticker.C:
			if err := compareOnce(ctx, reporter, cfg.Reader); err != nil {
				if errors.Is(err, platformconfig.ErrMissingFile) {
					logger.Info("config dual-read periodic stopped: baseline file disappeared",
						"path", cfg.ConfigPath)
					return nil
				}
				logger.Warn("config dual-read periodic sample failed", "err", err)
			}
		}
	}
}

// compareOnce 对每个已加载快照的 namespace 采样一次。
func compareOnce(ctx context.Context, reporter *Reporter, reader SnapshotReader) error {
	var errs []error
	for _, namespace := range reader.Namespaces() {
		snapshot, ok := reader.SnapshotFor(namespace)
		if !ok {
			// 尚未拉到该 namespace 的快照，跳过；下一周期再采样。
			continue
		}
		if err := reporter.ReportNamespace(ctx, namespace, snapshot.Values); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
