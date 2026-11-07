// Package lifecycle 提供进程级后台任务的收集与启动。
package lifecycle

import (
	"context"
	"log/slog"
	"sync"
)

// Tasks 收集装配期注册的后台任务；启动后不再接受注册。
type Tasks struct {
	mu    sync.Mutex
	tasks []func(context.Context) error
}

// NewTasks 创建空的任务收集器。
func NewTasks() *Tasks { return &Tasks{} }

// Add 注册一个后台任务；nil 忽略。
func (t *Tasks) Add(task func(context.Context) error) {
	if task == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tasks = append(t.tasks, task)
}

// List 返回当前任务快照。
func (t *Tasks) List() []func(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]func(context.Context) error(nil), t.tasks...)
}

// Start 启动任务，返回 stop：取消并等待全部退出。
//
// 任务错误只记录日志：后台任务失败不能把已经正常关闭的进程变成启动失败，
// 但必须可观测（日志 + 对应业务指标），否则 outbox 停摆会静默丢审计。
func Start(ctx context.Context, tasks []func(context.Context) error, logger *slog.Logger) func() {
	bgCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for _, task := range tasks {
		if task == nil {
			continue
		}
		wg.Add(1)
		go func(task func(context.Context) error) {
			defer wg.Done()
			if err := task(bgCtx); err != nil && bgCtx.Err() == nil {
				logger.Error("background task stopped", "err", err)
			}
		}(task)
	}
	return func() {
		cancel()
		wg.Wait()
	}
}
