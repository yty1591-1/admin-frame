package iamruntime_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/internal/platform"
)

// TestNewOutboxTaskRequiresDeps 验证缺少装配参数时失败关闭。
func TestNewOutboxTaskRequiresDeps(t *testing.T) {
	if _, err := iamruntime.NewOutboxTask(nil); err == nil {
		t.Fatal("expected error for nil deps")
	}
	if _, err := iamruntime.NewOutboxTask(&platform.Deps{}); err == nil {
		t.Fatal("expected error for missing config")
	}
}

// TestNewOutboxTaskDisabled 验证 outbox 关闭时 worker 不启动发布任务。
func TestNewOutboxTaskDisabled(t *testing.T) {
	deps := &platform.Deps{
		Config: &platform.Config{
			IAM: platform.IAMConfig{
				Outbox: platform.IAMOutboxConfig{Enabled: false},
			},
		},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	task, err := iamruntime.NewOutboxTask(deps)
	if err != nil {
		t.Fatalf("NewOutboxTask: %v", err)
	}
	if task != nil {
		t.Fatal("disabled outbox must not return a task")
	}
}

// TestNewOutboxTaskRequiresDatabase 验证 worker 发布器必须有持久化数据库。
func TestNewOutboxTaskRequiresDatabase(t *testing.T) {
	deps := &platform.Deps{
		Config: &platform.Config{
			IAM: platform.IAMConfig{
				Outbox: platform.IAMOutboxConfig{
					Enabled:      true,
					PollInterval: time.Second,
					BatchSize:    10,
					MaxAttempts:  3,
				},
			},
		},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	if _, err := iamruntime.NewOutboxTask(deps); err == nil {
		t.Fatal("expected error when database is missing")
	}
}
