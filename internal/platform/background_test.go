package platform

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// TestBackgroundTasksRunAndStop 验证后台任务随 stop 取消并等待退出。
func TestBackgroundTasksRunAndStop(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	registry := &backgroundTasks{}
	var started, stopped atomic.Int32
	registry.add(func(ctx context.Context) error {
		started.Add(1)
		<-ctx.Done()
		stopped.Add(1)
		return nil
	})
	registry.add(nil)

	tasks := registry.list()
	if len(tasks) != 1 {
		t.Fatalf("registered tasks = %d, want 1 (nil must be ignored)", len(tasks))
	}
	stop := startBackground(context.Background(), tasks, logger)
	deadline := time.Now().Add(time.Second)
	for started.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if started.Load() != 1 {
		t.Fatal("background task did not start")
	}
	stop()
	if stopped.Load() != 1 {
		t.Fatal("stop must wait for task to observe cancellation")
	}
}

// TestBackgroundTasksErrorIsLogged 验证任务失败不会 panic，也不会阻塞 stop。
func TestBackgroundTasksErrorIsLogged(_ *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	registry := &backgroundTasks{}
	registry.add(func(context.Context) error { return context.Canceled })
	stop := startBackground(context.Background(), registry.list(), logger)
	stop()
}
