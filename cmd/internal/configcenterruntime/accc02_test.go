package configcenterruntime

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/observability"
	"github.com/admin-frame/admin-frame/internal/platform"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/internal/platform/httpserver"
	"github.com/admin-frame/admin-frame/pkg/configclient"
)

// TestHotRateLimitTakesEffectOnPublish 是 AC-CC-02 的最小端到端取证：
// 一次快照刷新（发布）后，入口限流立即按新参数工作，进程没有重启。
//
// 取证路径：RefreshEvent → hotState 构建视图 → ApplyRuntimeParams 注入 →
// httpserver 管线读 RateLimitSource → 令牌桶改参。
func TestHotRateLimitTakesEffectOnPublish(t *testing.T) {
	hot, level, registry := newHotFixture(t)
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	publish := func(releaseID int64, values map[string]string) {
		hot.onEvent(configclient.RefreshEvent{
			Namespace: "platform", Loaded: true,
			Snapshot: configclient.Snapshot{ReleaseID: releaseID, Namespace: "platform", Values: values},
		}, logger, metrics, level)
	}

	// 初始发布：宽松限流，便于先建立桶。
	publish(1, map[string]string{"rate_limit.rps": "1000", "rate_limit.burst": "1000"})

	config := &platformconfig.Config{
		RateLimit: platformconfig.RateLimitConfig{
			Enabled: true,
			Driver:  platformconfig.RateLimitDriverMemory,
			Key:     platformconfig.RateLimitKeyChannel,
			RPS:     1, Burst: 1, // 静态值故意收紧，用于证明运行期读的是热更值
		},
	}
	deps := &platform.Deps{Config: config}
	(&Runtime{Hot: hot.holder}).ApplyRuntimeParams(deps)

	pipeline, err := httpserver.NewPipeline(httpserver.PipelineConfig{
		RateLimit:       config.RateLimit,
		RateLimitSource: deps.RateLimitParams,
	}, httpserver.PipelineDeps{Logger: logger})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	handler := pipeline(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	call := func() int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/configs/items", http.NoBody))
		return rec.Code
	}

	for i := 0; i < 5; i++ {
		if code := call(); code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 under burst=1000", i+1, code)
		}
	}

	// 二次发布：收紧到 burst=1。已消耗的令牌不退还，但当前令牌被截断到 1。
	publish(2, map[string]string{"rate_limit.rps": "1", "rate_limit.burst": "1"})
	if code := call(); code != http.StatusOK {
		t.Fatalf("first request after tightening = %d, want 200 (one clamped token)", code)
	}
	if code := call(); code != http.StatusTooManyRequests {
		t.Fatalf("second request after tightening = %d, want 429", code)
	}

	if got := hot.holder.Current().ReleaseID(); got != 2 {
		t.Fatalf("view release = %d, want 2", got)
	}
}

// TestStartupKeyPublishDoesNotChangeRuntime 验证非白名单键发布后不进入视图，
// 因而不会改变运行期参数（AC-CC-02 的反向断言）。
func TestStartupKeyPublishDoesNotChangeRuntime(t *testing.T) {
	hot, level, registry := newHotFixture(t)
	metrics, err := observability.New(registry)
	if err != nil {
		t.Fatalf("observability.New: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	hot.onEvent(configclient.RefreshEvent{
		Namespace: "platform", Loaded: true,
		Snapshot: configclient.Snapshot{
			ReleaseID: 1,
			Namespace: "platform",
			Values:    map[string]string{"rate_limit.burst": "50", "server.addr": ":9999"},
		},
	}, logger, metrics, level)

	config := &platformconfig.Config{RateLimit: staticRateLimit()}
	deps := &platform.Deps{Config: config}
	(&Runtime{Hot: hot.holder}).ApplyRuntimeParams(deps)

	params, _, _ := deps.RateLimitParams.Params()
	if params.Burst != 50 {
		t.Fatalf("burst = %d, want 50 from hot view", params.Burst)
	}
	// server.addr 不参与任何运行期参数：视图里根本没有这个键。
	if _, ok := hot.holder.Current().Lookup("server.addr"); ok {
		t.Fatal("startup key must not reach the hot view")
	}
}
