package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/config"
)

// TestFlatValuesMergesIncludes 验证 include 先加载、当前文件覆盖，并展平嵌套键。
func TestFlatValuesMergesIncludes(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.yaml")
	writeConfigFile(t, base, `
server:
  addr: ":8080"
  shutdown_timeout: 15s
tracing:
  insecure: false
features:
  - alpha
  - beta
`)
	main := filepath.Join(dir, "admin.yaml")
	writeConfigFile(t, main, `
include:
  - base.yaml
server:
  addr: ":9090"
http:
  max_header_bytes: 1048576
`)

	flat, err := config.FlatValues(main)
	if err != nil {
		t.Fatalf("FlatValues: %v", err)
	}
	cases := map[string]string{
		"server.addr":             ":9090",
		"server.shutdown_timeout": "15s",
		"tracing.insecure":        "false",
		"http.max_header_bytes":   "1048576",
		"features":                `["alpha","beta"]`,
	}
	for key, want := range cases {
		if got := flat[key]; got != want {
			t.Fatalf("flat[%q] = %q, want %q", key, got, want)
		}
	}
	if _, ok := flat["include"]; ok {
		t.Fatal("include must be stripped from flat values")
	}
}

// TestFlatValuesMissingFile 验证缺失文件返回可分类错误。
func TestFlatValuesMissingFile(t *testing.T) {
	if _, err := config.FlatValues(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("FlatValues(missing) error = nil, want error")
	}
}

func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
