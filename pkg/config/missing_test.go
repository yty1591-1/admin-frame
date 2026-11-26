package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/config"
)

type allowMissingConfig struct {
	Addr string `yaml:"addr"`
	Mode string `yaml:"mode"`
}

// TestLoaderAllowsMissingFileWithValues 验证无文件启动：文件缺失时用默认值 + 快照。
func TestLoaderAllowsMissingFileWithValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	loader, err := config.NewLoader(config.Options[allowMissingConfig]{
		Files:             []string{path},
		Values:            func() (map[string]string, error) { return map[string]string{"addr": ":9090"}, nil },
		AllowMissingFiles: true,
		Defaults:          func(cfg *allowMissingConfig) { cfg.Mode = "default" },
	})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9090" || cfg.Mode != "default" {
		t.Fatalf("cfg = %+v, want snapshot addr and default mode", cfg)
	}
}

// TestLoaderAppliesValueAliases 验证逻辑键可按别名写入历史顶层字段。
func TestLoaderAppliesValueAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.yaml")
	if err := os.WriteFile(path, []byte("addr: \":8080\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loader, err := config.NewLoader(config.Options[allowMissingConfig]{
		Files:        []string{path},
		Values:       func() (map[string]string, error) { return map[string]string{"server.addr": ":9090"}, nil },
		ValueAliases: map[string]string{"server.addr": "addr"},
	})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("addr = %q, want alias override", cfg.Addr)
	}
}

// TestLoaderRejectsMissingFileByDefault 验证默认行为不变，避免静默丢配置。
func TestLoaderRejectsMissingFileByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	loader, err := config.NewLoader(config.Options[allowMissingConfig]{Files: []string{path}})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	if _, err := loader.Load(); !errors.Is(err, config.ErrMissingFile) {
		t.Fatalf("Load error = %v, want ErrMissingFile", err)
	}
}
