package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/health"
	"github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// Deps 是装配期注入给 cmd 的平台能力集合。
//
// 只暴露“平台能力”，不暴露 repo / bus 等业务对象，避免变成 god object；
// 业务依赖由 cmd 在 Register 回调内自行构造。
type Deps struct {
	Config  *Config
	Logger  *slog.Logger
	Metrics *metrics.Metrics
	Tracer  *tracing.Provider
	Storage *StorageSet
	// AuthMW 是已按 channel 绑定的鉴权中间件。
	AuthMW func(http.Handler) http.Handler
	// RequirePermission 是权限中间件工厂，由 cmd 注入的 Authorizer 提供。
	RequirePermission auth.PermissionMiddleware
	// Health 用于 Register 阶段追加就绪检查（如仓储 ping）。
	Health *health.Handler
}

// Options 是 platform.Run 的装配参数。
type Options struct {
	// ConfigPath 是 yaml 路径；环境变量可覆盖其中字段（CFG-04）。
	ConfigPath string
	// Build 是构建身份；Service 为空时取配置中的 service。
	Build BuildInfo
	// NewAuthorizer 构造权限校验器；nil 时默认拒绝所有权限点（fail-closed）。
	NewAuthorizer func() *security.Authorizer
	// Register 注册本端 transport 与业务依赖；不得注册他端路由。
	Register func(*Deps, *http.ServeMux) error
}

// Run 完成一次完整的进程装配与运行：配置 → 日志 → 指标 → 追踪 → 存储 →
// 鉴权 → 请求链路 → 探针 → 监听 → 优雅关闭 → 资源释放。
//
// 任一阶段失败返回 error，由 cmd 决定退出码；本函数不使用 log.Fatal / os.Exit，
// 保证 defer 清理一定执行（REL-02）。
func Run(ctx context.Context, opts Options) (err error) {
	if opts.Register == nil {
		return errors.New("platform: Options.Register is required")
	}
	cfg, err := LoadConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := NewLogger(cfg)

	met, err := metrics.New(metrics.Options{
		Service: cfg.Service,
		Channel: cfg.Channel,
		Env:     cfg.Env,
	})
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	build := opts.Build
	if build.Service == "" {
		build.Service = cfg.Service
	}
	if err := met.RegisterBuildInfo(build.Version, build.Commit, build.BuildTime); err != nil {
		return fmt.Errorf("metrics build info: %w", err)
	}

	tp, err := tracing.Init(ctx, cfg.TracingOptions(build.Version))
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := tp.Shutdown(shCtx); shutdownErr != nil && err == nil {
			err = fmt.Errorf("tracing shutdown: %w", shutdownErr)
		}
	}()

	stores, err := OpenStorageSet(ctx, cfg.Storage)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() {
		if closeErr := stores.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("storage close: %w", closeErr)
		}
	}()

	authMW, err := NewAuthMiddleware(cfg.Auth, auth.Channel(cfg.Channel), cfg.Env)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	authorizer := security.NewAuthorizer(nil)
	if opts.NewAuthorizer != nil {
		if authorizer = opts.NewAuthorizer(); authorizer == nil {
			authorizer = security.NewAuthorizer(nil)
		}
	}

	hh := health.New()
	registerStorageChecks(hh, stores)

	deps := &Deps{
		Config:            cfg,
		Logger:            logger,
		Metrics:           met,
		Tracer:            tp,
		Storage:           stores,
		AuthMW:            authMW,
		RequirePermission: auth.PermissionMiddleware(authorizer.Require),
		Health:            hh,
	}

	api := http.NewServeMux()
	if err := opts.Register(deps, api); err != nil {
		return fmt.Errorf("register routes: %w", err)
	}

	root := http.NewServeMux()
	MountHealth(root, hh)
	MountVersion(root, build)
	MountMetrics(root, met.Handler())
	root.Handle("/", api)

	pipeline, err := NewPipeline(PipelineConfig{
		Timeout:        cfg.HTTP.Timeout,
		BodyLimitBytes: cfg.HTTP.BodyLimitBytes,
		RateLimit:      cfg.RateLimit,
		CORS:           cfg.CORS,
	}, PipelineDeps{
		Logger:  logger,
		Metrics: met,
		Tracer:  tp,
	})
	if err != nil {
		return err
	}

	logger.Info("process starting",
		"service", cfg.Service,
		"channel", cfg.Channel,
		"env", cfg.Env,
		"version", build.Version,
	)
	return ListenAndServe(ctx, cfg, pipeline(root), logger)
}

// registerStorageChecks 为默认桶与命名桶注册就绪探测。
func registerStorageChecks(hh *health.Handler, stores *StorageSet) {
	hh.Register("storage", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return stores.ReadyAll(ctx)
	})
	for _, name := range stores.Names() {
		n := name
		st, ok := stores.Get(n)
		if !ok {
			continue
		}
		hh.Register("storage:"+n, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return storage.Ready(ctx, st)
		})
	}
}
