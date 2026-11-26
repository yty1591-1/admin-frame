package config_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/config"
)

func TestBindValuesAppliesTypedFields(t *testing.T) {
	var cfg testConfig
	err := config.BindValues(&cfg, map[string]string{
		"addr":                        "0.0.0.0:9000",
		"shutdown_timeout":            "45s",
		"ratio":                       "0.75",
		"enabled":                     "yes",
		"tags":                        "edge, cn",
		"log.level":                   "debug",
		"auth.mode":                   "jwt",
		"storage.s3.force_path_style": "true",
	})
	if err != nil {
		t.Fatalf("bind values: %v", err)
	}
	if cfg.Addr != "0.0.0.0:9000" || cfg.ShutdownTimeout != 45*time.Second {
		t.Fatalf("scalar fields not applied: %+v", cfg)
	}
	if cfg.Ratio == nil || *cfg.Ratio != 0.75 {
		t.Fatalf("pointer float not applied: %+v", cfg.Ratio)
	}
	if cfg.Enabled == nil || !*cfg.Enabled {
		t.Fatalf("pointer bool not applied: %+v", cfg.Enabled)
	}
	if len(cfg.Tags) != 2 || cfg.Tags[1] != "cn" {
		t.Fatalf("slice not applied: %v", cfg.Tags)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("nested struct not applied: %+v", cfg.Log)
	}
	if cfg.Auth == nil || cfg.Auth.Mode != "jwt" {
		t.Fatalf("pointer branch not allocated: %+v", cfg.Auth)
	}
	if !cfg.Storage.S3.ForcePathStyle {
		t.Fatal("deeply nested field not applied")
	}
}

func TestBindValuesAppliesJSONArraySlice(t *testing.T) {
	var cfg testConfig
	err := config.BindValues(&cfg, map[string]string{
		"tags": `["admin","biz","c","worker","internal"]`,
	})
	if err != nil {
		t.Fatalf("bind JSON array slice: %v", err)
	}
	want := []string{"admin", "biz", "c", "worker", "internal"}
	if len(cfg.Tags) != len(want) {
		t.Fatalf("tags=%v, want %v", cfg.Tags, want)
	}
	for i := range want {
		if cfg.Tags[i] != want[i] {
			t.Fatalf("tags=%v, want %v", cfg.Tags, want)
		}
	}
}

func TestBindValuesAllowsEmptyString(t *testing.T) {
	cfg := testConfig{Addr: ":8080"}
	if err := config.BindValues(&cfg, map[string]string{"addr": ""}); err != nil {
		t.Fatalf("bind empty value: %v", err)
	}
	if cfg.Addr != "" {
		t.Fatalf("empty snapshot value must override: %q", cfg.Addr)
	}
}

func TestBindValuesIgnoresUnknownAndUnsupportedKeys(t *testing.T) {
	cfg := testConfig{Addr: ":8080", Meta: map[string]string{"keep": "1"}}
	err := config.BindValues(&cfg, map[string]string{
		"unknown.key": "x",
		"meta.keep":   "2",
	})
	if err != nil {
		t.Fatalf("unknown keys must be ignored: %v", err)
	}
	if cfg.Addr != ":8080" || cfg.Meta["keep"] != "1" {
		t.Fatalf("unexpected mutation: %+v", cfg)
	}
}

func TestBindValuesRejectsInvalidValueWithoutLeakingIt(t *testing.T) {
	var cfg testConfig
	err := config.BindValues(&cfg, map[string]string{"shutdown_timeout": "not-a-secret-value"})
	if !errors.Is(err, config.ErrSnapshotOverride) {
		t.Fatalf("bind invalid = %v, want ErrSnapshotOverride", err)
	}
	if strings.Contains(err.Error(), "not-a-secret-value") {
		t.Fatalf("error must not leak raw value: %v", err)
	}
}

func TestBindValuesSkipsYAMLExcludedField(t *testing.T) {
	type target struct {
		Ignored string `yaml:"-"`
		Kept    string `yaml:"kept"`
	}
	var cfg target
	if err := config.BindValues(&cfg, map[string]string{"ignored": "x", "kept": "y"}); err != nil {
		t.Fatalf("bind values: %v", err)
	}
	if cfg.Ignored != "" || cfg.Kept != "y" {
		t.Fatalf("yaml exclusion not honored: %+v", cfg)
	}
}

func TestLoaderSnapshotOverridesEnvAndFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":9090\"\nlog:\n  level: warn\n")
	opts := testOptions(path)
	// 与生产一致：仅放行基础设施段；业务键 APP_ADDR 不得压过配置中心快照（CFG-04）。
	opts.EnvPathAllowlist = []string{"db", "redis"}
	opts.LookupEnv = envLookup(map[string]string{"APP_ADDR": ":7070"})
	opts.Values = func() (map[string]string, error) {
		return map[string]string{"addr": ":6060"}, nil
	}
	cfg := loadConfig(t, newLoader(t, opts))
	if cfg.Addr != ":6060" {
		t.Fatalf("snapshot must win: addr = %q, want :6060", cfg.Addr)
	}
	if cfg.Log.Level != "warn" {
		t.Fatalf("keys absent from snapshot must keep file value: %q", cfg.Log.Level)
	}
}

func TestLoaderSnapshotErrorFailsLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":9090\"\n")
	opts := testOptions(path)
	opts.Values = func() (map[string]string, error) {
		return nil, errors.New("snapshot unavailable")
	}
	if _, err := newLoader(t, opts).Load(); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("load = %v, want ErrInvalidConfig", err)
	}
}
