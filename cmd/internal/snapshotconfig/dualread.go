package snapshotconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/configcenter/app"
	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	platformconfig "github.com/admin-frame/admin-frame/pkg/config"
	pkgmetrics "github.com/admin-frame/admin-frame/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

// Reporter 是迁移期双读（CC-F-42）的可复用入口。
//
// 与一次性 Report 的区别：文件基线与指标注册只做一次，之后可按 namespace 反复
// 对比。启动期对比与运行期周期对比因此共用同一组指标，不会重复注册。
type Reporter struct {
	configPath string
	env        string
	registry   *spec.Registry
	reporter   *dualReadReporter

	mu       sync.Mutex
	baseline map[string]string
}

// NewReporter 构造 Reporter；registerer 为 nil 时只做对比与日志，不打点。
func NewReporter(configPath, env string, logger *slog.Logger,
	registerer prometheus.Registerer) (*Reporter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	registry, err := spec.Platform()
	if err != nil {
		return nil, err
	}
	reporter := &dualReadReporter{logger: logger, lastLog: make(map[string]string)}
	if registerer != nil {
		metrics, metricsErr := observability.NewDualRead(registerer)
		if metricsErr != nil {
			return nil, metricsErr
		}
		reporter.metrics = metrics
	}
	return &Reporter{
		configPath: configPath,
		env:        env,
		registry:   registry,
		reporter:   reporter,
	}, nil
}

// ReportAll 对比全部给定 namespace（启动期用法）。
func (r *Reporter) ReportAll(ctx context.Context, values map[string]map[string]string) error {
	var errs []error
	for _, namespace := range sortedNamespaces(values) {
		if err := r.ReportNamespace(ctx, namespace, values[namespace]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReportNamespace 对比单个 namespace 的已声明键（运行期周期用法）。
//
// 无文件基线时返回 platformconfig.ErrMissingFile，由调用方决定是否停止对比。
func (r *Reporter) ReportNamespace(ctx context.Context, namespace string,
	centerValues map[string]string) error {
	flat, err := r.flat()
	if err != nil {
		return err
	}
	baseline := make(map[string]string)
	center := make(map[string]string)
	for _, keySpec := range r.registry.List(namespace) {
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
	reader, err := app.NewDualReader(staticProvider{values: center}, r.reporter)
	if err != nil {
		return err
	}
	result, err := reader.Resolve(ctx, r.env, namespace, baseline)
	r.reporter.summary(namespace, result)
	return err
}

// flat 返回并缓存文件基线取值；无文件时返回 platformconfig.ErrMissingFile。
func (r *Reporter) flat() (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.baseline != nil {
		return r.baseline, nil
	}
	flat, err := platformconfig.FlatValues(r.configPath)
	if err != nil {
		return nil, err
	}
	r.baseline = flat
	return flat, nil
}

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
	reporter, err := NewReporter(configPath, env, logger, registerer)
	if err != nil {
		return err
	}
	// 无文件启动是 M1 演练目标：没有基线可对比，不视为启动错误。
	if _, err := reporter.flat(); err != nil {
		if errors.Is(err, platformconfig.ErrMissingFile) {
			return nil
		}
		return err
	}
	return reporter.ReportAll(ctx, startup.ByNamespace)
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

// sortedNamespaces 返回稳定的 namespace 顺序，保证日志与指标可复现。
func sortedNamespaces(values map[string]map[string]string) []string {
	namespaces := make([]string, 0, len(values))
	for namespace := range values {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

// staticProvider 把已验证的签名快照当作双读的「中心」数据源。
type staticProvider struct {
	values map[string]string
}

func (p staticProvider) Snapshot(context.Context, string, string) (app.Snapshot, error) {
	return app.Snapshot{Values: p.values}, nil
}

// dualReadReporter 把差异转成指标与结构化日志；metrics 可为 nil。
//
// 指标每次刷新（Gauge 幂等）；日志只在差异集合或计数变化时输出，
// 否则 5s 轮询 / 30s 周期对比会把同一条差异刷成噪音。
type dualReadReporter struct {
	logger  *slog.Logger
	metrics *observability.DualReadMetrics

	mu      sync.Mutex
	lastLog map[string]string
}

func (r *dualReadReporter) ReportDualRead(_ context.Context, _, namespace string,
	result app.DualReadResult) {
	mismatched := make(map[string]struct{}, len(result.Mismatches)+len(result.MissingInCenter))
	for _, diff := range result.Mismatches {
		mismatched[diff.Key] = struct{}{}
		if r.metrics != nil {
			r.metrics.SetMismatch(namespace, diff.Key, true)
		}
	}
	for _, key := range result.MissingInCenter {
		// 文件有、中心无：迁移未覆盖，同样阻断切换。
		mismatched[key] = struct{}{}
		if r.metrics != nil {
			r.metrics.SetMismatch(namespace, key, true)
		}
	}
	for key := range result.Values {
		if _, ok := mismatched[key]; ok {
			continue
		}
		if r.metrics != nil {
			r.metrics.SetMismatch(namespace, key, false)
		}
	}
	if r.metrics != nil {
		r.metrics.SetDegraded(namespace, result.Degraded)
	}

	if !r.shouldLog(namespace, mismatchSignature(result)) {
		return
	}
	for _, diff := range result.Mismatches {
		r.logger.Warn("config dual-read mismatch",
			"namespace", namespace, "key", diff.Key,
			"baseline_hash", diff.BaselineHash, "center_hash", diff.CenterHash)
	}
	for _, key := range result.MissingInCenter {
		r.logger.Warn("config dual-read key missing in center", "namespace", namespace, "key", key)
	}
	for _, key := range result.MissingInBaseline {
		r.logger.Info("config dual-read new key in center", "namespace", namespace, "key", key)
	}
}

// summary 输出一行可检索的汇总，便于 7 天零差异观察与验收取证。
//
// 与差异明细分开去重：即使差异集合不变，计数变化（降级、版本推进）也要留痕。
func (r *dualReadReporter) summary(namespace string, result app.DualReadResult) {
	signature := summarySignature(result)
	if !r.shouldLog(namespace+"#summary", signature) {
		return
	}
	r.logger.Info("config dual-read summary",
		"namespace", namespace,
		"degraded", result.Degraded,
		"mismatch", len(result.Mismatches),
		"missing_in_center", len(result.MissingInCenter),
		"missing_in_baseline", len(result.MissingInBaseline),
		"release_id", result.ReleaseID,
		"version", result.Version)
}

// shouldLog 判断给定签名相对上次是否变化；首次一律输出。
func (r *dualReadReporter) shouldLog(namespace, signature string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.lastLog[namespace]; ok && previous == signature {
		return false
	}
	r.lastLog[namespace] = signature
	return true
}

// mismatchSignature 把差异集合压成稳定签名（含哈希，值变化也能识别）。
func mismatchSignature(result app.DualReadResult) string {
	parts := make([]string, 0, len(result.Mismatches)+len(result.MissingInCenter))
	for _, diff := range result.Mismatches {
		parts = append(parts, "mismatch:"+diff.Key+":"+diff.BaselineHash+":"+diff.CenterHash)
	}
	for _, key := range result.MissingInCenter {
		parts = append(parts, "missing:"+key)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// summarySignature 用计数与版本构造汇总签名。
func summarySignature(result app.DualReadResult) string {
	return fmt.Sprintf("degraded=%t;mismatch=%d;missing=%d;new=%d;release=%d;version=%d",
		result.Degraded, len(result.Mismatches), len(result.MissingInCenter),
		len(result.MissingInBaseline), result.ReleaseID, result.Version)
}
