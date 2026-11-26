package appconfig_test

import (
	"path/filepath"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
)

// TestLoadConfigExampleFiles 在 M3 删除 configs/ 样例后改为校验无文件启动路径。
//
// 历史行为：逐一加载 prod/*.yaml.example。样例删除后契约由 key spec、
// TestLoadWithoutConfigFile 与 configcenter 演练覆盖。
func TestLoadConfigExampleFiles(t *testing.T) {
	files, err := filepath.Glob("../../../configs/prod/*.yaml.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("M3: configs/prod yaml.example removed; covered by nofile + key spec tests")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			if _, err := appconfig.Load(file); err != nil {
				t.Fatalf("load %s: %v", file, err)
			}
		})
	}
}
