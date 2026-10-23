package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/internal/platform"

	// 消费端按需访问对象存储；不需要时可移除该 import 以缩小二进制。
	_ "github.com/admin-frame/admin-frame/pkg/storage/all"
)

// 由 -ldflags -X 注入（RELS-03）；本地默认 dev。
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "configs/worker.yaml", "config file path")
	flag.Parse()
	if err := run(*configPath); err != nil {
		log.Printf("worker: %v", err)
		os.Exit(1)
	}
}

// run 装配 worker 依赖并阻塞消费，直到收到退出信号。
//
// 使用 error 返回而非 log.Fatal，保证 defer（storage.Close / stop）一定执行。
func run(configPath string) error {
	cfg, err := platform.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := platform.NewLogger(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := platform.OpenStorage(ctx, cfg.Storage)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			logger.Warn("storage close", "err", closeErr)
		}
	}()

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
	outboxTask, err := iamruntime.NewOutboxTask(deps)
	if err != nil {
		return fmt.Errorf("iam outbox: %w", err)
	}
	var wg sync.WaitGroup
	if outboxTask != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if runErr := outboxTask(ctx); runErr != nil && ctx.Err() == nil {
				logger.Error("iam outbox publisher stopped", "err", runErr)
			}
		}()
	}
	defer wg.Wait()

	logger.Info("worker started",
		"poll_interval", cfg.Worker.PollInterval.String(),
		"storage_driver", cfg.Storage.Driver,
		"db_driver", cfg.DB.Driver,
		"outbox_enabled", outboxTask != nil,
		"version", version,
		"commit", commit,
		"build_time", buildTime,
	)
	ticker := time.NewTicker(cfg.Worker.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return nil
		case <-ticker.C:
			// 异步任务运行时尚未接入（见 ENT-03）；此处仅维持进程心跳。
			logger.Debug("worker idle; async runtime pending ENT-03")
		}
	}
}
