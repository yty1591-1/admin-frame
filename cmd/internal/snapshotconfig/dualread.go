package snapshotconfig

import (
	"context"
	"errors"
	"log/slog"
	"sort"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	platformconfig "github.com/admin-frame/admin-frame/pkg/config"
	pkgmetrics "github.com/admin-frame/admin-frame/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

// Report 执行迁移期双读对比（CC-F-42）：文件基线 vs 每个启用 namespace 的签名快照。
//
// 只比较各 namespace 中已在代码声明的键，避免把 DSN / 密钥等不可迁移键误报
// 为遗漏。差异写指标并记录 key 与哈希，不记录原始取值；任一 namespace 对比
// 失败只返回错误供调用方记录，不阻断启动。
func Report(ctx context.Context, configPath, env string, startup *Startup,
	logger *slog.Logger, registerer prometheus.Registerer) error {
	if startup == nil || len(startup.ByNamespace) == 0 {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	registry, err := spec.Platform()
	if err != nil {
		return err
	}
	flat, err := platformconfig.FlatValues(configPath)
	if err != nil {
		// 无文件启动是 M1 演练目标：没有基线可对比，不视为启动错误。
		if errors.Is(err, platformconfig.ErrMissingFile) {
			return nil
		}
		return err
	}
	reporter := &dualReadReporter{logger: logger}
	if registerer != nil {
		metrics, metricsErr := observability.NewDualRead(registerer)
		if metricsErr != nil {
			return metricsErr
		}
		reporter.metrics = metrics
	}
	var errs []error
	for _, namespace := range sortedNamespaces(startup.ByNamespace) {
		if err := compareNamespace(ctx, flat, env, namespace, startup.ByNamespace[namespace],
			registry, reporter); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// compareNamespace 对比单个 namespace 的已声明键。
func compareNamespace(ctx context.Context, flat map[string]string, env, namespace string,
	centerValues map[string]string, registry *spec.Registry, reporter *dualReadReporter) error {
	baseline := make(map[string]string)
	center := make(map[string]string)
	for _, keySpec := range registry.List(namespace) {
		if value, ok := flat[spec.YAMLPath(keySpec.Key)]; ok {
			baseline[keySpec.Key] = value
		}
		if value, ok := centerValues[keySpec.Key]; ok {
			center[keySpec.Key] = value
		}
	}
	if len(baseline) == 0 && len(center) == 0 {
		return nil
	}
	reader, err := app.NewDualReader(staticProvider{values: center}, reporter)
	if err != nil {
		return err
	}
	result, err := reader.Resolve(ctx, env, namespace, baseline)
	reporter.summary(namespace, result)
	return err
}

// sortedNamespaces 返回稳定的 namespace 顺序，保证日志与指标可复现。
func sortedNamespaces(values map[string]map[string]string) []string {
	namespaces := make([]string, 0, len(values))
	for namespace := range values {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

// ReportWithMetrics 是 Report 的便捷入口，供 cmd 直接传入进程指标。
//
// metrics 为 nil 时仍执行对比与日志，只是不打点。
func ReportWithMetrics(ctx context.Context, configPath, env string, startup *Startup,
	logger *slog.Logger, metrics *pkgmetrics.Metrics) error {
	var registerer prometheus.Registerer
	if metrics != nil {
		registerer = metrics.Registerer()
	}
	return Report(ctx, configPath, env, startup, logger, registerer)
}

// staticProvider 把已验证的签名快照当作双读的「中心」数据源。
type staticProvider struct {
	values map[string]string
}

func (p staticProvider) Snapshot(context.Context, string, string) (app.Snapshot, error) {
	return app.Snapshot{Values: p.values}, nil
}

// dualReadReporter 把差异转成指标与结构化日志；metrics 可为 nil。
type dualReadReporter struct {
	logger  *slog.Logger
	metrics *observability.DualReadMetrics
}

func (r *dualReadReporter) ReportDualRead(_ context.Context, _, namespace string,
	result app.DualReadResult) {
	mismatched := make(map[string]struct{}, len(result.Mismatches)+len(result.MissingInCenter))
	for _, diff := range result.Mismatches {
		mismatched[diff.Key] = struct{}{}
		if r.metrics != nil {
			r.metrics.SetMismatch(namespace, diff.Key, true)
		}
		r.logger.Warn("config dual-read mismatch",
			"namespace", namespace, "key", diff.Key,
			"baseline_hash", diff.BaselineHash, "center_hash", diff.CenterHash)
	}
	for _, key := range result.MissingInCenter {
		// 文件有、中心无：迁移未覆盖，同样阻断切换。
		mismatched[key] = struct{}{}
		if r.metrics != nil {
			r.metrics.SetMismatch(namespace, key, true)
		}
		r.logger.Warn("config dual-read key missing in center", "namespace", namespace, "key", key)
	}
	for key := range result.Values {
		if _, ok := mismatched[key]; ok {
			continue
		}
		if r.metrics != nil {
			r.metrics.SetMismatch(namespace, key, false)
		}
	}
	for _, key := range result.MissingInBaseline {
		r.logger.Info("config dual-read new key in center", "namespace", namespace, "key", key)
	}
	if r.metrics != nil {
		r.metrics.SetDegraded(namespace, result.Degraded)
	}
}

// summary 输出一行可检索的汇总，便于 7 天零差异观察与验收取证。
func (r *dualReadReporter) summary(namespace string, result app.DualReadResult) {
	r.logger.Info("config dual-read summary",
		"namespace", namespace,
		"degraded", result.Degraded,
		"mismatch", len(result.Mismatches),
		"missing_in_center", len(result.MissingInCenter),
		"missing_in_baseline", len(result.MissingInBaseline),
		"release_id", result.ReleaseID,
		"version", result.Version)
}
