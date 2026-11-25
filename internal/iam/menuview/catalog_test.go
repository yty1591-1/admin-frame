package menuview_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// TestSeedComponentsRegistered 反向校验 seed 中的页面组件都已登记在白名单。
//
// seed 里存在大量 disabled 页面菜单（启用前必须先建 .vue），正向扫描只能发现
// 白名单指向缺失文件，无法发现「seed 引用了白名单外的 component」——那种菜单
// 一旦启用就会在前端落到 not-found。这里以 migrations 为事实源做双向校验。
func TestSeedComponentsRegistered(t *testing.T) {
	root := repoRoot(t)
	migrations, err := os.OpenRoot(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatalf("open migrations: %v", err)
	}
	t.Cleanup(func() { _ = migrations.Close() })
	files, err := fs.Glob(migrations.FS(), "*.up.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("未找到 migrations/*.up.sql")
	}
	views, err := os.OpenRoot(filepath.Join(root, "frontend", "apps", "web-admin", "src", "views"))
	if err != nil {
		t.Fatalf("open views: %v", err)
	}
	t.Cleanup(func() { _ = views.Close() })
	componentPattern := regexp.MustCompile(`'/[A-Za-z0-9_/.-]+\.vue'`)
	catalog := menuview.Catalog{}
	for _, file := range files {
		data, readErr := migrations.ReadFile(file)
		if readErr != nil {
			t.Fatalf("read %s: %v", file, readErr)
		}
		for _, raw := range componentPattern.FindAllString(string(data), -1) {
			component := strings.Trim(raw, "'")
			switch {
			case strings.HasPrefix(component, "/member/"):
				// B 端菜单不开放组件登记，占位仅用于路由/面包屑文本。
				continue
			case component == "/system/config/index.vue":
				// 000032 起该节点升级为 catalog（component 置空、redirect 到 items）。
				continue
			}
			if !catalog.Allows(domain.ChannelAdmin, component) {
				t.Errorf("%s: seed component %q 未登记在 adminComponents", filepath.Base(file), component)
				continue
			}
			path := filepath.FromSlash(strings.TrimPrefix(component, "/"))
			if _, statErr := views.Stat(path); errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("%s: seed component %q 对应文件不存在: %s", filepath.Base(file), component, path)
			} else if statErr != nil {
				t.Errorf("stat %s: %v", path, statErr)
			}
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
