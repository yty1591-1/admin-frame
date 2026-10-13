package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/admin-frame/admin-frame/internal/order/infrastructure/eventbus"
	"github.com/admin-frame/admin-frame/internal/platform"
)

func main() {
	configPath := flag.String("config", "configs/worker.yaml", "config file path")
	flag.Parse()

	cfg, err := platform.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	logger := platform.NewLogger(cfg)
	bus := eventbus.NewMemoryBus(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("worker started", "poll_interval", cfg.Worker.PollInterval.String())
	ticker := time.NewTicker(cfg.Worker.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return
		case <-ticker.C:
			events := bus.Drain()
			for _, e := range events {
				logger.Info("process domain event", "event", e.EventName(), "at", e.OccurredAt())
			}
			if len(events) == 0 {
				logger.Debug("no events")
			}
		}
	}
}
