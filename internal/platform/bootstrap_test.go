package platform_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// TestRunRequiresRegister 验证缺少路由注册时装配期失败。
func TestRunRequiresRegister(t *testing.T) {
	err := platform.Run(context.Background(), platform.Options{ConfigPath: "unused.yaml"})
	if err == nil || !strings.Contains(err.Error(), "Register") {
		t.Fatalf("err=%v want Register required", err)
	}
}

// TestRunMissingConfig 验证配置文件缺失时返回可诊断错误。
func TestRunMissingConfig(t *testing.T) {
	err := platform.Run(context.Background(), platform.Options{
		ConfigPath: "../../configs/does-not-exist.yaml",
		Register:   func(*platform.Deps, *http.ServeMux) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "load config") {
		t.Fatalf("err=%v want load config error", err)
	}
}

// TestRunRejectsInvalidStorage 验证装配期校验失败会阻止进程启动。
func TestRunRejectsInvalidStorage(t *testing.T) {
	path := writeConfig(t, "bad-storage.yaml", "service: c-api\nchannel: c\naddr: \"127.0.0.1:0\"\nstorage:\n  driver: bogus\n")
	err := platform.Run(context.Background(), platform.Options{
		ConfigPath: path,
		Register:   func(*platform.Deps, *http.ServeMux) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "storage.driver") {
		t.Fatalf("err=%v want storage driver error", err)
	}
}

// TestRunReturnsRegisterError 验证 Register 错误向上传递（不被吞掉）。
func TestRunReturnsRegisterError(t *testing.T) {
	path := writeConfig(t, "c.yaml", "service: c-api\nchannel: c\naddr: \"127.0.0.1:0\"\n")
	want := errors.New("boom")
	err := platform.Run(context.Background(), platform.Options{
		ConfigPath: path,
		Register: func(*platform.Deps, *http.ServeMux) error {
			return want
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v want %v", err, want)
	}
}

// TestRunBootstrapsAndShutsDown 验证完整装配链路（配置→日志→指标→追踪→存储→
// 鉴权→探针→链路→监听→关闭）在 ctx 取消后干净退出。
func TestRunBootstrapsAndShutsDown(t *testing.T) {
	path := writeConfig(t, "c.yaml", "service: c-api\nchannel: c\naddr: \"127.0.0.1:0\"\nhttp:\n  timeout: 1s\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := platform.Run(ctx, platform.Options{
		ConfigPath: path,
		Build:      platform.BuildInfo{Version: "test"},
		Register: func(deps *platform.Deps, mux *http.ServeMux) error {
			if deps.Config == nil || deps.Logger == nil || deps.Metrics == nil ||
				deps.Tracer == nil || deps.Storage == nil || deps.AuthMW == nil ||
				deps.RequirePermission == nil || deps.Health == nil {
				return errors.New("deps not fully wired")
			}
			// 默认拒绝策略：未注入 Authorizer 时权限中间件必须返回 403/401 而非放行。
			mux.Handle("GET /deny", deps.AuthMW(deps.RequirePermission("anything")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}
