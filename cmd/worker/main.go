package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/cmd/internal/identityruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
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

	deps := &platform.Deps{Config: cfg, Logger: logger, Database: database}
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
	logger.Info("worker shutting down")
	return nil
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
