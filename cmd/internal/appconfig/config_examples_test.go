package appconfig_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/appconfig"
)

// TestLoadConfigExampleFiles 逐一加载仓库内示例配置，保证模块 include 与结构体同步。
//
// 示例为 prod 基线，密钥通过环境变量注入，使其与真实启动走同一条严格解码与校验路径。
func TestLoadConfigExampleFiles(t *testing.T) {
	t.Setenv("ADMIN_FRAME_AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ADMIN_FRAME_DB_DSN", "user:pass@tcp(localhost:3306)/db?parseTime=true&loc=UTC")
	t.Setenv("ADMIN_FRAME_REDIS_ADDR", "localhost:6379")
	t.Setenv("ADMIN_FRAME_IDENTITY_HASH_KEY", strings.Repeat("a", 64))
	t.Setenv("ADMIN_FRAME_IDENTITY_MFA_SECRET_KEY", strings.Repeat("b", 32))
	t.Setenv("ADMIN_FRAME_STORAGE_S3_ACCESS_KEY", "access")
	t.Setenv("ADMIN_FRAME_STORAGE_S3_SECRET_KEY", "secret")
	t.Setenv("ADMIN_FRAME_IDENTITY_SMS_DAILY_BUDGET", "100")

	files, err := filepath.Glob("../../../configs/prod/*.yaml.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		// M1 已删除非密钥端样例；加载契约改由 key spec / configcenter 演练覆盖。
		t.Skip("M1: prod channel yaml.example removed; covered by configcenter drill")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			if _, err := appconfig.Load(file); err != nil {
				t.Fatalf("load %s: %v", file, err)
			}
		})
	}
}
