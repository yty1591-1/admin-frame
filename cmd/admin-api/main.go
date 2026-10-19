package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	filesapp "github.com/admin-frame/admin-frame/internal/files/app"
	filesadmin "github.com/admin-frame/admin-frame/internal/files/transport/admin"
	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	admintransport "github.com/admin-frame/admin-frame/internal/order/transport/admin"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/health"
	"github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/storage"
	"github.com/admin-frame/admin-frame/pkg/tracing"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/admin.yaml", "config file path")
	flag.Parse()

	cfg, err := platform.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	logger := platform.NewLogger(cfg)

	repo := persistence.NewMemoryRepository()
	bus := eventbus.NewMemoryBus(logger)
	svc := app.NewOrderService(repo, bus)
	orderH := admintransport.NewHandler(svc)

	authMW, err := platform.NewAuthMiddleware(cfg.Auth, auth.ChannelAdmin)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}

	met, err := metrics.New(metrics.Options{
		Service: cfg.Service,
		Channel: cfg.Channel,
		Env:     cfg.Env,
	})
	if err != nil {
		log.Fatalf("metrics: %v", err)
	}
	met.MustRegisterBuildInfo(version, commit, buildTime)

	ctx := context.Background()
	store, err := platform.OpenStorage(ctx, cfg.Storage)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer func() { _ = store.Close() }()

	fileSvc := filesapp.NewService(store, filesapp.Config{
		MaxBytes:      cfg.Storage.MaxObjectBytes,
		PresignPutTTL: cfg.Storage.PresignPutTTL,
		PresignGetTTL: cfg.Storage.PresignGetTTL,
	})
	filesH := filesadmin.NewHandler(fileSvc)

	api := http.NewServeMux()
	admintransport.Register(api, orderH, authMW)
	filesadmin.Register(api, filesH, authMW)

	tp, err := tracing.Init(ctx, cfg.TracingOptions(version))
	if err != nil {
		log.Fatalf("tracing: %v", err)
	}
	exitCode := 0
	defer func() {
		_ = tp.Shutdown(context.Background()) // 进程退出前刷出剩余 span
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()

	root := http.NewServeMux()
	hh := health.New()
	hh.Register("memory_repo", repo.Ping)
	hh.Register("storage", func() error { return storage.Ready(context.Background(), store) })
	platform.MountHealth(root, hh)
	platform.MountVersion(root, platform.BuildInfo{
		Service:   cfg.Service,
		Version:   version,
		Commit:    commit,
		BuildTime: buildTime,
	})
	platform.MountMetrics(root, met.Handler())
	root.Handle("/", api)

	handler := platform.Chain(root,
		platform.Recover(logger),
		tracing.HTTPMiddleware(tracing.WithTracer(tp.Tracer(""))),
		platform.RequestID(),
		met.HTTP().Middleware(),
		platform.AccessLog(logger),
	)

	if err := platform.ListenAndServe(ctx, cfg.Addr, handler, cfg.ShutdownTimeout, logger); err != nil {
		logger.Error("server failed", "err", err)
		exitCode = 1
	}
}
