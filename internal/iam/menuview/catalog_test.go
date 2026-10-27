package menuview_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/iam/menuview"
)

// repoRoot 从测试文件位置回溯到仓库根目录。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// TestAdminComponentsExist 扫描前端 views 目录，保证白名单中的每个 component 都有对应文件。
//
// 这条测试是「后端清单 + CI 扫描前端 views」的落地点：后端允许了不存在的页面时直接失败。
func TestAdminComponentsExist(t *testing.T) {
	viewsRoot := filepath.Join(repoRoot(t), "frontend", "apps", "web-admin", "src", "views")
	catalog := menuview.Catalog{}
	components := catalog.Components(domain.ChannelAdmin)
	if len(components) == 0 {
		t.Fatal("admin component 白名单为空")
	}
	for _, component := range components {
		if !strings.HasPrefix(component, "/") || !strings.HasSuffix(component, ".vue") {
			t.Fatalf("component %q 必须以 / 开头且以 .vue 结尾", component)
		}
		if strings.Contains(component, "..") {
			t.Fatalf("component %q 不得包含路径穿越", component)
		}
		path := filepath.Join(viewsRoot, filepath.FromSlash(strings.TrimPrefix(component, "/")))
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			t.Fatalf("component %q 对应文件不存在: %s", component, path)
		} else if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}

// TestBizHasNoComponents 验证白名单只服务 A 端：B 端菜单不开放 CRUD。
func TestBizHasNoComponents(t *testing.T) {
	catalog := menuview.Catalog{}
	if got := catalog.Components(domain.ChannelBiz); len(got) != 0 {
		t.Fatalf("biz components = %v, want empty", got)
	}
	if catalog.Allows(domain.ChannelBiz, "/member/list/index.vue") {
		t.Fatal("biz 不应允许登记 component")
	}
}
