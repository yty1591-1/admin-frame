package configcenterruntime

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
)

// TestNewRefreshSkipsMemoryDatabase 验证内存模式不启动动态刷新。
func TestNewRefreshSkipsMemoryDatabase(t *testing.T) {
	runtime, err := NewRefresh(&platform.Deps{Config: &platformconfig.Config{Env: "dev"}})
	if err != nil {
		t.Fatalf("NewRefresh: %v", err)
	}
	if runtime != nil {
		t.Fatalf("NewRefresh(memory) = %+v, want nil", runtime)
	}
}

// TestNewRefreshRequiresDeps 验证缺失平台依赖时装配失败。
func TestNewRefreshRequiresDeps(t *testing.T) {
	if _, err := NewRefresh(nil); err == nil {
		t.Fatal("NewRefresh(nil) error = nil, want error")
	}
}
