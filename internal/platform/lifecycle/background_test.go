package lifecycle

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// TestTasksRunAndStop 验证后台任务随 stop 取消并等待退出。
func TestTasksRunAndStop(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	registry := NewTasks()
	var started, stopped atomic.Int32
	registry.Add(func(ctx context.Context) error {
		started.Add(1)
		<-ctx.Done()
		stopped.Add(1)
		return nil
	})
	registry.Add(nil)

	tasks := registry.List()
	if len(tasks) != 1 {
		t.Fatalf("registered tasks = %d, want 1 (nil must be ignored)", len(tasks))
	}
	stop := Start(context.Background(), tasks, logger)
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

// TestTasksErrorIsLogged 验证任务失败不会 panic，也不会阻塞 stop。
func TestTasksErrorIsLogged(_ *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	registry := NewTasks()
	registry.Add(func(context.Context) error { return context.Canceled })
	stop := Start(context.Background(), registry.List(), logger)
	stop()
}
