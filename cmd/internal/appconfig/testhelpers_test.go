package appconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testJWTSecret 返回长度恰为 32 字节的 HMAC 测试密钥。
//
// 与 internal/platform 的测试 helper 语义一致：避免高熵字面量触发
// gitleaks generic-api-key 误报；fill 取不同值可得到互不相等的密钥。
func testJWTSecret(fill byte) string {
	return strings.Repeat(string(fill), 32)
}

// writeConfig 把 YAML 内容写入临时文件并返回路径。
func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
