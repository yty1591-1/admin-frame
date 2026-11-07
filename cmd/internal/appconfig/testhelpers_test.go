package appconfig_test

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig 把 YAML 内容写入临时文件并返回路径。
func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
