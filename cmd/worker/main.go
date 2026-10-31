package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/db"
	pkgmetrics "github.com/admin-frame/admin-frame/pkg/metrics"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/worker.yaml", "config file path")
	replayDLQ := flag.String("replay-dlq", "", "replay dead letters: source event id or 'all'")
	flag.Parse()
	if err := run(*configPath, *replayDLQ); err != nil {
		log.Printf("worker: %v", err)
		os.Exit(1)
	}
}

// run 装配 worker 依赖并阻塞消费，直到收到退出信号。
//
// worker 是 Outbox Relay 的独占宿主：注册业务 topic handler 后运行 Relay；
// -replay-dlq 模式只做死信重放后退出，不启动 Relay（见 docs/runbooks/async-dlq.md）。
func run(configPath, replayDLQ string) error {
	cfg, err := platform.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := platform.NewLogger(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := platform.OpenDatabase(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			logger.Warn("db close", "err", closeErr)
		}
	}()

	met, err := pkgmetrics.New(pkgmetrics.Options{
		Service: cfg.Service,
		Channel: cfg.Channel,
		Env:     cfg.Env,
	})
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	deps := &platform.Deps{Config: cfg, Logger: logger, Database: database, Metrics: met}
	if replayDLQ != "" {
		return replayDeadLetters(ctx, deps, replayDLQ, logger)
	}

	asyncRuntime, err := platform.NewAsyncRuntime(deps)
	if err != nil {
		return fmt.Errorf("async runtime: %w", err)
	}
	if asyncRuntime == nil {
		return fmt.Errorf("worker requires async.enabled=true")
	}
	deps.Async = asyncRuntime
	if err := iamruntime.RegisterAsync(deps); err != nil {
		return fmt.Errorf("register async handlers: %w", err)
	}
	if err := identityruntime.RegisterAsync(deps); err != nil {
		return fmt.Errorf("register identity async handlers: %w", err)
	}
	cleanupHandle, err := identityruntime.StartCleanup(ctx, deps)
	if err != nil {
		return fmt.Errorf("identity cleanup: %w", err)
	}

	// worker 不承载业务流量，但需要暴露清理指标与探针，供 OPS-02 观测与 ServiceMonitor 抓取。
	stopHTTP := startWorkerHTTP(cfg, database, met, logger)
	defer stopHTTP()

	logger.Info("worker started",
		"db_driver", cfg.DB.Driver,
		"async_driver", cfg.Async.Driver,
		"poll_interval", cfg.Async.Outbox.PollInterval.String(),
		"concurrency", cfg.Async.Outbox.Concurrency,
		"version", version,
		"commit", commit,
		"build_time", buildTime,
	)
	if err := asyncRuntime.Run(ctx); err != nil {
		return err
	}
	// 优雅停机：等待在途清理批次完成，避免归档事务被取消（REL-08）。
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stopCancel()
	if stopErr := cleanupHandle.Stop(stopCtx); stopErr != nil {
		logger.Warn("identity cleanup did not finish before shutdown", "error", stopErr)
	}
	logger.Info("worker shutting down")
	return nil
}

// startWorkerHTTP 启动 worker 的指标与探针端点，返回幂等的优雅关闭函数。
//
// 只暴露 /metrics、/healthz 与 /readyz；不挂载任何业务路由。
func startWorkerHTTP(
	cfg *platform.Config,
	database *db.DB,
	met *pkgmetrics.Metrics,
	logger *slog.Logger,
) func() {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", met.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := database.Ping(pingCtx); err != nil {
			http.Error(w, "database not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	addr := cfg.Addr
	if addr == "" {
		addr = ":8090"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("worker http server stopped", "error", err)
		}
	}()
	logger.Info("worker http server started", "addr", addr)
	return func() {
		timeout := cfg.ShutdownTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Warn("worker http server shutdown", "error", err)
		}
	}
}

// replayDeadLetters 把死信重置为 pending，由后续 Relay 重新投递。
func replayDeadLetters(ctx context.Context, deps *platform.Deps, target string, logger *slog.Logger) error {
	if deps.Database == nil {
		return fmt.Errorf("replay-dlq requires a persistent database")
	}
	store := platform.NewAsyncStore(deps.Database)
	replayer, ok := store.(outbox.DeadLetterReplayer)
	if !ok {
		return fmt.Errorf("async store does not support dead letter replay")
	}
	now := time.Now().UTC()
	if target == "all" {
		replayed, err := replayer.ReplayAllDeadLetters(ctx, now)
		if err != nil {
			return fmt.Errorf("replay all dead letters: %w", err)
		}
		logger.Info("dead letters replayed", "count", replayed)
		return nil
	}
	replayed, err := replayer.ReplayDeadLetter(ctx, target, now)
	if err != nil {
		return fmt.Errorf("replay dead letter %s: %w", target, err)
	}
	if !replayed {
		return fmt.Errorf("dead letter %s not found or already replayed", target)
	}
	logger.Info("dead letter replayed", "source_id", target)
	return nil
}
