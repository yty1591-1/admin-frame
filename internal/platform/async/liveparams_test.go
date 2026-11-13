package async

import (
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/outbox"
)

func newParamsRuntime() *Runtime {
	return &Runtime{cfg: &config.Config{Async: config.AsyncConfig{
		Outbox: config.AsyncOutboxConfig{
			PollInterval: 1 * time.Second,
			BatchSize:    100,
			Lease:        30 * time.Second,
			MaxAttempts:  10,
		},
		Consumer: config.AsyncConsumerConfig{
			VisibilityTimeout: 60 * time.Second,
			RetryBackoff:      config.AsyncBackoffFixed,
		},
	}}}
}

// TestOutboxParamsMirrorConfigWithoutLive 验证未注入热更来源时等于装配期配置。
func TestOutboxParamsMirrorConfigWithoutLive(t *testing.T) {
	params := newParamsRuntime().currentOutboxParams()
	if params.PollInterval != time.Second || params.BatchSize != 100 ||
		params.Lease != 30*time.Second || params.MaxAttempts != 10 {
		t.Fatalf("params = %+v, want config values", params)
	}
	if params.Backoff != outbox.Backoff(config.AsyncBackoffFixed) {
		t.Fatalf("backoff = %q, want %q", params.Backoff, config.AsyncBackoffFixed)
	}
}

// TestOutboxParamsApplyHotOverrides 验证热更值逐项覆盖，未发布的项保持配置值。
func TestOutboxParamsApplyHotOverrides(t *testing.T) {
	runtime := newParamsRuntime()
	runtime.SetLiveParams(LiveParams{
		Outbox: func() outbox.Params {
			return outbox.Params{
				PollInterval: 5 * time.Second,
				MaxAttempts:  3,
				Backoff:      outbox.BackoffExponential,
			}
		},
	})

	params := runtime.currentOutboxParams()
	if params.PollInterval != 5*time.Second || params.MaxAttempts != 3 {
		t.Fatalf("hot overrides not applied: %+v", params)
	}
	// 未覆盖的项必须保持配置值，而不是被清零。
	if params.BatchSize != 100 || params.Lease != 30*time.Second {
		t.Fatalf("untouched fields changed: %+v", params)
	}
	if params.Backoff != outbox.BackoffExponential {
		t.Fatalf("backoff = %q, want exponential", params.Backoff)
	}
}

// TestOutboxParamsIgnoreInvalidOverrides 验证 0 / 空值被视为「未发布」，
// 避免坏热更把 batch_size 打成 0 后 relay 空转。
func TestOutboxParamsIgnoreInvalidOverrides(t *testing.T) {
	runtime := newParamsRuntime()
	runtime.SetLiveParams(LiveParams{
		Outbox: func() outbox.Params { return outbox.Params{} },
	})
	params := runtime.currentOutboxParams()
	if params.BatchSize != 100 || params.PollInterval != time.Second || params.Lease != 30*time.Second {
		t.Fatalf("zero overrides must be ignored: %+v", params)
	}
}

// TestVisibilityTracksLiveParams 验证消费租约窗口按次读取，非法值回落配置。
func TestVisibilityTracksLiveParams(t *testing.T) {
	runtime := newParamsRuntime()
	if got := runtime.currentVisibility(); got != 60*time.Second {
		t.Fatalf("visibility = %v, want config value", got)
	}

	window := 90 * time.Second
	runtime.SetLiveParams(LiveParams{Visibility: func() time.Duration { return window }})
	if got := runtime.currentVisibility(); got != 90*time.Second {
		t.Fatalf("visibility = %v, want 90s", got)
	}

	window = 0
	if got := runtime.currentVisibility(); got != 60*time.Second {
		t.Fatalf("invalid visibility must fall back to config, got %v", got)
	}
}

// TestNilReceiverSafe 验证未装配 async 时调用不 panic（API 进程可能未启用 async）。
func TestNilReceiverSafe(t *testing.T) {
	var runtime *Runtime
	if got := runtime.currentVisibility(); got != 0 {
		t.Fatalf("nil runtime visibility = %v, want 0", got)
	}
	if params := runtime.currentOutboxParams(); params != (outbox.Params{}) {
		t.Fatalf("nil runtime params = %+v, want zero", params)
	}
	runtime.SetLiveParams(LiveParams{})
}
