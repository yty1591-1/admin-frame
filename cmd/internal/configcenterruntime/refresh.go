package configcenterruntime

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// RefreshConfig 是进程内动态刷新客户端的装配参数。
type RefreshConfig struct {
	// Source 是快照来源，通常是 app.SnapshotService（MySQL / 内存实现）；必填。
	Source configclient.Source
	// PublicKeys 是 CONFIGCENTER_SNAPSHOT_PUBLIC_KEYS 原值；为空表示关闭动态刷新。
	PublicKeys string
	// SnapshotFile 是 CONFIGCENTER_SNAPSHOT_FILE；可选，刷新成功后原子落盘，
	// 供下次冷启动验签装载。
	SnapshotFile string
	// Env 与 Namespace 是消费的快照归属；必填。
	Env       string
	Namespace string
	// OnRefresh 可选回调，用于运行期日志与指标。
	OnRefresh func(configclient.RefreshEvent)
}

// NewRefreshClient 构造进程内动态刷新客户端。
//
// 未配置公钥时返回 (nil, nil)：动态刷新关闭，进程继续使用启动期配置；
// 配置了公钥但格式非法则返回错误，不做「静默降级为不验签」。
func NewRefreshClient(cfg RefreshConfig) (*configclient.Client, error) {
	rawKeys := strings.TrimSpace(cfg.PublicKeys)
	if rawKeys == "" {
		return nil, nil
	}
	keys, err := configclient.ParsePublicKeys(rawKeys)
	if err != nil {
		return nil, err
	}
	verifier, err := configclient.NewVerifier(keys)
	if err != nil {
		return nil, err
	}
	var store configclient.SnapshotStore
	if path := strings.TrimSpace(cfg.SnapshotFile); path != "" {
		store, err = configclient.NewFileStore(path)
		if err != nil {
			return nil, err
		}
	}
	return configclient.NewClient(configclient.ClientConfig{
		Source:    cfg.Source,
		Verifier:  verifier,
		Env:       cfg.Env,
		Namespace: cfg.Namespace,
		Store:     store,
		OnRefresh: cfg.OnRefresh,
	})
}

// StartRefresh 注册进程内动态刷新后台任务。
//
// interval <= 0 时取 configclient.DefaultRefreshInterval（5s ±20%）；
// 未启用动态刷新（Client=nil）或运行时不具备后台任务注册能力时为空操作。
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
			"env", r.clientEnv, "namespace", r.clientNamespace, "interval", interval)
	}
	deps.Background(func(ctx context.Context) error {
		return runRefresh(ctx, client, interval, logger)
	})
}

// runRefresh 持续刷新直到 ctx 取消；正常关停不视为任务失败。
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

// refreshLogger 构造刷新状态日志。
//
// 只在发布版本变化或错误变化时输出，避免 5s 轮询刷屏；日志只含快照元数据，
// 不含取值。回调可能被并发 Refresh 触发，内部状态需加锁。
func refreshLogger(logger *slog.Logger) func(configclient.RefreshEvent) {
	if logger == nil {
		return nil
	}
	var (
		mu          sync.Mutex
		loaded      bool
		lastRelease int64
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
		if loaded && event.Snapshot.ReleaseID == lastRelease {
			return
		}
		loaded = true
		lastRelease = event.Snapshot.ReleaseID
		logger.Info("configcenter snapshot loaded",
			"env", event.Snapshot.Env,
			"namespace", event.Snapshot.Namespace,
			"release_id", event.Snapshot.ReleaseID,
			"release_no", event.Snapshot.ReleaseNo,
			"key_id", event.Snapshot.KeyID,
			"keys", len(event.Snapshot.Values))
	}
}
