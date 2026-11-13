package configcenterruntime

import (
	"log/slog"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	"github.com/admin-frame/admin-frame/pkg/configclient"
	"github.com/admin-frame/admin-frame/pkg/hotconfig"
)

// hotState 维护按 namespace 的最近快照取值与进程级热更视图（ADR-0015）。
//
// 视图是「所有启用 namespace 的白名单键合并结果」：多数业务键只在单个
// namespace 出现，合并后读方无需关心键属于哪个 namespace。
type hotState struct {
	specs    []hotconfig.Spec
	hotKeys  map[string]struct{}
	declared map[string]struct{}
	holder   *hotconfig.Holder

	mu      sync.Mutex
	values  map[string]map[string]string
	merged  map[string]string
	version int64
}

// newHotState 构造热更状态；白名单与键声明不一致时直接失败（fail fast）。
func newHotState() (*hotState, error) {
	specs, err := spec.HotSpecs()
	if err != nil {
		return nil, err
	}
	declared, err := spec.DeclaredKeySet()
	if err != nil {
		return nil, err
	}
	initial, err := hotconfig.Build(specs, nil, 0, 0, time.Now())
	if err != nil {
		return nil, err
	}
	return &hotState{
		specs:    specs,
		hotKeys:  spec.HotKeySet(),
		declared: declared,
		holder:   hotconfig.NewHolder(initial),
		values:   make(map[string]map[string]string),
		merged:   map[string]string{},
	}, nil
}

// onEvent 在一次快照刷新后重建视图。
//
// 失败语义（ADR-0015 决策 5）：任何构建错误都保留上一份视图，
// 只记 rejected 指标与日志，绝不回落到代码默认值。
func (h *hotState) onEvent(event configclient.RefreshEvent, logger *slog.Logger,
	metrics *observability.Metrics, logLevel *slog.LevelVar) {
	if h == nil || event.Err != nil || !event.Loaded {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	namespace := event.Namespace
	if namespace == "" {
		namespace = spec.NamespacePlatform
	}
	h.values[namespace] = event.Snapshot.Values
	merged := make(map[string]string, len(h.merged))
	for _, values := range h.values {
		for key, value := range values {
			merged[key] = value
		}
	}

	// 已声明但不可热更的键发生变化时计数，便于发现「以为会热更」的误用。
	for key, value := range merged {
		if _, hot := h.hotKeys[key]; hot {
			continue
		}
		if _, declared := h.declared[key]; !declared {
			continue
		}
		if previous, ok := h.merged[key]; ok && previous != value {
			metrics.ObserveHotIgnored(key)
		}
	}

	previous := h.merged
	h.merged = merged

	nextVersion := h.version + 1
	view, err := hotconfig.Build(h.specs, merged, nextVersion, event.Snapshot.ReleaseID, time.Now())
	if err != nil {
		metrics.ObserveHotReload("rejected")
		if logger != nil {
			logger.Error("configcenter hot view rejected; keeping previous view",
				"namespace", namespace, "err", err)
		}
		return
	}
	if !h.holder.Store(view) {
		metrics.ObserveHotReload("unchanged")
		return
	}
	h.version = nextVersion
	metrics.ObserveHotReload("applied")
	metrics.SetHotViewVersion(namespace, nextVersion)
	for _, key := range view.Keys() {
		if value, ok := view.Lookup(key); ok {
			if old, existed := previous[key]; !existed || old != value {
				metrics.ObserveHotKeyApplied(key)
			}
		}
	}
	applyLogLevel(view, logLevel)
	if logger != nil {
		logger.Info("configcenter hot view applied",
			"version", nextVersion, "release_id", event.Snapshot.ReleaseID,
			"namespace", namespace, "keys", len(view.Keys()))
	}
}

// applyLogLevel 把 hot 视图中的 log.level 应用到进程级别开关。
func applyLogLevel(view *hotconfig.View, level *slog.LevelVar) {
	if view == nil || level == nil {
		return
	}
	raw, ok := view.Lookup("log.level")
	if !ok {
		return
	}
	switch raw {
	case "debug":
		level.Set(slog.LevelDebug)
	case "info":
		level.Set(slog.LevelInfo)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	}
}
