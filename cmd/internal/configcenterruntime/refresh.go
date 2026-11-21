package configcenterruntime

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// RefreshConfig 是进程内动态刷新客户端的装配参数（兼容旧调用）。
type RefreshConfig struct {
	Source           configclient.Source
	PublicKeys       string // 已忽略（ADR-0016）
	SnapshotFile     string // 已忽略
	Env              string
	Namespace        string
	Namespaces       []string
	PrimaryNamespace string
	OnRefresh        func(configclient.RefreshEvent)
	OnWatchDropped   func()
	Notifier         configclient.Notifier
	Cache            *configclient.DirCache
}

// NewRefreshClient 构造动态刷新客户端；Verifier 不再必填。
func NewRefreshClient(cfg RefreshConfig) (*configclient.Client, error) {
	if cfg.Source == nil {
		return nil, nil
	}
	return configclient.NewClient(configclient.ClientConfig{
		Source:           cfg.Source,
		Env:              cfg.Env,
		Namespace:        cfg.Namespace,
		Namespaces:       cfg.Namespaces,
		PrimaryNamespace: cfg.PrimaryNamespace,
		Notifier:         cfg.Notifier,
		Cache:            cfg.Cache,
		OnRefresh:        cfg.OnRefresh,
		OnWatchDropped:   cfg.OnWatchDropped,
	})
}

// StartRefresh 注册动态刷新后台任务（长轮询或 interval 轮询）。
func (r *Runtime) StartRefresh(deps *platform.Deps, interval time.Duration) {
	if r == nil || r.Client == nil || deps == nil || deps.Background == nil {
		return
	}
	if interval <= 0 {
		interval = configclient.DefaultRefreshInterval
	}
	client := r.Client
	logger := deps.Logger
	if logger != nil {
		logger.Info("configcenter dynamic refresh enabled",
			"env", r.clientEnv, "namespaces", strings.Join(r.clientNamespaces, ","),
			"interval", interval)
	}
	deps.Background(func(ctx context.Context) error {
		return runRefresh(ctx, client, interval, logger)
	})
}

// StartRefreshContext 在调用方提供的 ctx 上启动刷新。
func (r *Runtime) StartRefreshContext(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	if r == nil || r.Client == nil {
		return
	}
	if interval <= 0 {
		interval = configclient.DefaultRefreshInterval
	}
	client := r.Client
	if logger != nil {
		logger.Info("configcenter dynamic refresh enabled",
			"env", r.clientEnv, "namespaces", strings.Join(r.clientNamespaces, ","),
			"interval", interval)
	}
	go func() {
		_ = runRefresh(ctx, client, interval, logger)
	}()
}

func runRefresh(ctx context.Context, client *configclient.Client, interval time.Duration, logger *slog.Logger) error {
	err := client.Run(ctx, interval)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if logger != nil {
			logger.Info("configcenter dynamic refresh stopped")
		}
		return nil
	}
	return err
}

func clientRefreshObserver(logger *slog.Logger, metrics *observability.Metrics) func(configclient.RefreshEvent) {
	log := refreshLogger(logger)
	if log == nil && metrics == nil {
		return nil
	}
	var coldStart sync.Once
	return func(event configclient.RefreshEvent) {
		if log != nil {
			log(event)
		}
		if metrics == nil {
			return
		}
		coldStart.Do(func() {
			if !event.Loaded && event.Err != nil {
				metrics.ClientColdStartDegraded()
			}
		})
		metrics.ObserveClientRefresh(event)
	}
}

func refreshLogger(logger *slog.Logger) func(configclient.RefreshEvent) {
	if logger == nil {
		return nil
	}
	var (
		mu          sync.Mutex
		lastRelease = map[string]int64{} // namespace → 最近一次已打 INFO 的 release_id
		lastErr     string
	)
	return func(event configclient.RefreshEvent) {
		mu.Lock()
		defer mu.Unlock()
		if event.Err != nil {
			message := event.Err.Error()
			if message == lastErr {
				return
			}
			lastErr = message
			logger.Warn("configcenter snapshot refresh failed; serving previous snapshot",
				"degraded", event.Degraded, "error", message)
			return
		}
		lastErr = ""
		if !event.Loaded {
			return
		}
		ns := strings.TrimSpace(event.Snapshot.Namespace)
		// 多 namespace 轮询时各自 release_id 不同；按 namespace 去重，
		// 避免 5s 一轮把 6 个 namespace 的「未变更」重复打成 INFO。
		if prev, ok := lastRelease[ns]; ok && prev == event.Snapshot.ReleaseID {
			return
		}
		lastRelease[ns] = event.Snapshot.ReleaseID
		logger.Info("configcenter snapshot loaded",
			"env", event.Snapshot.Env,
			"namespace", event.Snapshot.Namespace,
			"release_id", event.Snapshot.ReleaseID,
			"release_no", event.Snapshot.ReleaseNo,
			"keys", len(event.Snapshot.Values))
	}
}
