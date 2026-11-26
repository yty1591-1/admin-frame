package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/pkg/health"
)

// SetupOnlyAutoExitEnv 控制降级态在安装完成后是否自动退出。
//
// 默认自动退出（=false 时只打印日志继续服务）：容器编排据此把进程重启成
// 正常启动，从而拿到完整配置；无编排的环境可关闭自动退出后手工重启。
const SetupOnlyAutoExitEnv = "ADMIN_FRAME_SETUP_ONLY_AUTO_EXIT"

const (
	defaultSetupOnlyPollInterval = 5 * time.Second
	defaultSetupOnlyShutdown     = 15 * time.Second
)

// SetupOnlyOptions 是降级态（setup-only）的装配参数（ADR-0018 决策 2）。
//
// 降级态只暴露存活/就绪/版本探针与安装向导代理：不装配数据库、鉴权、
// 业务路由与限流，因此不依赖任何业务配置——这正是缺键时仍能修复的前提。
type SetupOnlyOptions struct {
	Service string
	Channel string
	Env     string
	Addr    string
	Build   httpserver.BuildInfo
	// MissingKeys 是未满足的必填键（namespace/key），写入首行日志与 /readyz。
	MissingKeys []string
	// Mount 注册降级态允许的路由（安装向导代理）；业务路由一律不注册。
	Mount func(*http.ServeMux)
	// Ready 轮询安装态；返回 true 表示安装已完成。nil 表示不轮询。
	Ready func(context.Context) (bool, error)
	// PollInterval 轮询间隔；≤0 用 5s。
	PollInterval time.Duration
	// ShutdownTimeout 优雅关闭上限；≤0 用 15s。
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
}

// RunSetupOnly 以最小 HTTP 面运行进程，直到 ctx 取消、收到退出信号或
// （自动退出开启时）安装完成。
func RunSetupOnly(ctx context.Context, opts SetupOnlyOptions) error {
	if strings.TrimSpace(opts.Addr) == "" {
		return errors.New("platform: setup-only requires addr")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	missing := strings.Join(opts.MissingKeys, ", ")
	log.Warn("SETUP-ONLY MODE: missing required keys, install wizard required",
		"service", opts.Service,
		"channel", opts.Channel,
		"env", opts.Env,
		"addr", opts.Addr,
		"missing_keys", missing,
	)

	root := newSetupOnlyMux(opts, missing)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go watchInstallReady(runCtx, cancel, opts, log)

	shutdown := opts.ShutdownTimeout
	if shutdown <= 0 {
		shutdown = defaultSetupOnlyShutdown
	}
	cfg := &platformconfig.Config{
		Env:             opts.Env,
		Service:         opts.Service,
		Channel:         opts.Channel,
		Addr:            opts.Addr,
		ShutdownTimeout: shutdown,
		HTTP: platformconfig.HTTPConfig{
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			Timeout:           30 * time.Second,
		},
	}
	return httpserver.ListenAndServe(runCtx, cfg, root, log)
}

// newSetupOnlyMux 组装降级态路由：探针 + 版本 + 调用方挂载的安装代理。
//
// 故意不挂 /metrics 与任何业务路由：降级态没有可信的鉴权与限流，暴露面
// 越大越危险（ADR-0018「安全与失效」）。
func newSetupOnlyMux(opts SetupOnlyOptions, missing string) *http.ServeMux {
	probes := health.New()
	probes.Register("config_install", func() error {
		return fmt.Errorf("config_incomplete: missing required keys: %s; run admin install wizard", missing)
	})
	api := http.NewServeMux()
	if opts.Mount != nil {
		opts.Mount(api)
	}
	root := http.NewServeMux()
	httpserver.MountHealth(root, probes)
	httpserver.MountVersion(root, opts.Build)
	root.Handle("/", api)
	return root
}

// watchInstallReady 轮询安装态，ready 后按开关取消服务（进程退出码 0）。
func watchInstallReady(ctx context.Context, stop context.CancelFunc, opts SetupOnlyOptions, log *slog.Logger) {
	if opts.Ready == nil {
		return
	}
	interval := opts.PollInterval
	if interval <= 0 {
		interval = defaultSetupOnlyPollInterval
	}
	autoExit := !strings.EqualFold(strings.TrimSpace(os.Getenv(SetupOnlyAutoExitEnv)), "false")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ready, err := opts.Ready(ctx)
			if err != nil {
				log.Warn("setup-only: poll install status failed", "err", err)
				continue
			}
			if !ready {
				continue
			}
			if !autoExit {
				log.Info("setup-only: install completed; restart manually to leave setup-only mode",
					"auto_exit_env", SetupOnlyAutoExitEnv)
				continue
			}
			log.Info("setup-only: install completed, exiting for normal startup")
			stop()
			return
		}
	}
}
