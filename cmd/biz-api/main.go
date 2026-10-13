package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/admin-frame/admin-frame/internal/order/app"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/order/infrastructure/persistence"
	biztransport "github.com/admin-frame/admin-frame/internal/order/transport/biz"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/health"
	"github.com/admin-frame/admin-frame/pkg/metrics"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/biz.yaml", "config file path")
	flag.Parse()

	cfg, err := platform.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	logger := platform.NewLogger(cfg)

	repo := persistence.NewMemoryRepository()
	bus := eventbus.NewMemoryBus(logger)
	svc := app.NewOrderService(repo, bus)
	h := biztransport.NewHandler(svc)

	authMW, err := platform.NewAuthMiddleware(cfg.Auth, auth.ChannelBiz)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	api := biztransport.NewRouter(h, authMW)

	met, err := metrics.New(metrics.Options{
		Service: cfg.Service,
		Channel: cfg.Channel,
		Env:     cfg.Env,
	})
	if err != nil {
		log.Fatalf("metrics: %v", err)
	}
	met.MustRegisterBuildInfo(version, commit, buildTime)

	root := http.NewServeMux()
	hh := health.New()
	hh.Register("memory_repo", repo.Ping)
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
		platform.RequestID(),
		met.HTTP().Middleware(),
		platform.AccessLog(logger),
	)

	if err := platform.ListenAndServe(context.Background(), cfg.Addr, handler, cfg.ShutdownTimeout, logger); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}
