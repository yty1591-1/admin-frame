package config_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/config"
	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml",
		"addr: \":8081\"\n"+
			"log:\n"+
			"  level: debug\n")
	opts := testOptions(path)
	opts.LookupEnv = envLookup(map[string]string{"APP_ADDR": ":9090"})

	cfg, err := config.NewLoader(opts)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	loaded, err := cfg.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Addr != ":9090" {
		t.Fatalf("addr=%q want env override", loaded.Addr)
	}
	if loaded.Log.Level != "debug" {
		t.Fatalf("log.level=%q want file value", loaded.Log.Level)
	}
}

func TestLoadDefaultsOnly(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "empty.yaml", "")

	cfg := loadConfig(t, newLoader(t, testOptions(path)))
	if cfg.Addr != ":8080" || cfg.Log.Level != "info" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadLayeredFiles(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.yaml",
		"addr: \":8081\"\n"+
			"log:\n"+
			"  level: warn\n"+
			"  format: json\n")
	override := writeFile(t, dir, "override.yaml", "addr: \":8082\"\n")
	cfg := loadConfig(t, newLoader(t, testOptions(base, override)))
	if cfg.Addr != ":8082" {
		t.Fatalf("addr=%q want later file override", cfg.Addr)
	}
	if cfg.Log.Level != "warn" || cfg.Log.Format != "json" {
		t.Fatalf("earlier file fields lost: %+v", cfg.Log)
	}
}

func TestLoadStrictUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "unknown_key: 1\n")
	_, err := newLoader(t, testOptions(path)).Load()
	if !errors.Is(err, config.ErrUnknownField) {
		t.Fatalf("err=%v want ErrUnknownField", err)
	}
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
	if !pkgerrors.IsCode(err, pkgerrors.CodeInvalidArgument) {
		t.Fatalf("code=%s want invalid_argument", pkgerrors.CodeOf(err))
	}
}

func TestLoadAllowUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "unknown_key: 1\naddr: \":8081\"\n")
	opts := testOptions(path)
	opts.AllowUnknownFields = true
	cfg := loadConfig(t, newLoader(t, opts))
	if cfg.Addr != ":8081" {
		t.Fatalf("addr=%q", cfg.Addr)
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	_, err := newLoader(t, testOptions(path)).Load()
	if !errors.Is(err, config.ErrMissingFile) {
		t.Fatalf("err=%v want ErrMissingFile", err)
	}
	if !pkgerrors.IsCode(err, pkgerrors.CodeNotFound) {
		t.Fatalf("code=%s want not_found", pkgerrors.CodeOf(err))
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	_, err := newLoader(t, testOptions(dir)).Load()
	if !errors.Is(err, config.ErrFileUnreadable) {
		t.Fatalf("err=%v want ErrFileUnreadable", err)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: [unclosed\n")
	_, err := newLoader(t, testOptions(path)).Load()
	if !errors.Is(err, config.ErrMalformedFile) {
		t.Fatalf("err=%v want ErrMalformedFile", err)
	}
}

func TestLoadRedactsValuesInErrors(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "ratio: s3cr3t-value\n")
	_, err := newLoader(t, testOptions(path)).Load()
	if err == nil {
		t.Fatal("expected decode error")
	}
	if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("error leaks config value: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("error should mark redaction: %v", err)
	}
}

func TestLoadRejectsMultipleDocuments(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n---\naddr: \":8082\"\n")
	_, err := newLoader(t, testOptions(path)).Load()
	if !errors.Is(err, config.ErrMalformedFile) {
		t.Fatalf("err=%v want ErrMalformedFile", err)
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadValidationFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \"\"\n")
	opts := testOptions(path)
	opts.Defaults = nil
	_, err := newLoader(t, opts).Load()
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
	if !errors.Is(err, errAddrRequired) {
		t.Fatalf("err=%v want validation sentinel", err)
	}
	if !pkgerrors.IsCode(err, pkgerrors.CodeInvalidArgument) {
		t.Fatalf("code=%s want invalid_argument", pkgerrors.CodeOf(err))
	}
}

func TestLoadEnvTypes(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "")
	opts := testOptions(path)
	opts.LookupEnv = envLookup(map[string]string{
		"APP_ENV":                         "prod",
		"APP_SHUTDOWN_TIMEOUT":            "1m30s",
		"APP_RATIO":                       "0.25",
		"APP_ENABLED":                     "yes",
		"APP_TAGS":                        " a , b ,, c ",
		"APP_LOG_LEVEL":                   "error",
		"APP_AUTH_MODE":                   "demo",
		"APP_STORAGE_S3_ENDPOINT":         "localhost:9000",
		"APP_STORAGE_S3_FORCE_PATH_STYLE": "1",
		"TEST_ONLY_CUSTOM":                "custom-value",
		"APP_IGNORED":                     "must-not-apply",
	})
	cfg := loadConfig(t, newLoader(t, opts))

	if cfg.Env != "prod" {
		t.Fatalf("env=%q", cfg.Env)
	}
	if cfg.ShutdownTimeout != 90*time.Second {
		t.Fatalf("shutdown_timeout=%s", cfg.ShutdownTimeout)
	}
	if cfg.Ratio == nil || *cfg.Ratio != 0.25 {
		t.Fatalf("ratio=%v", cfg.Ratio)
	}
	if cfg.Enabled == nil || !*cfg.Enabled {
		t.Fatalf("enabled=%v", cfg.Enabled)
	}
	if len(cfg.Tags) != 3 || cfg.Tags[0] != "a" || cfg.Tags[2] != "c" {
		t.Fatalf("tags=%v", cfg.Tags)
	}
	if cfg.Log.Level != "error" {
		t.Fatalf("log.level=%q", cfg.Log.Level)
	}
	if cfg.Auth == nil || cfg.Auth.Mode != "demo" {
		t.Fatalf("auth=%+v", cfg.Auth)
	}
	if cfg.Storage.S3.Endpoint != "localhost:9000" || !cfg.Storage.S3.ForcePathStyle {
		t.Fatalf("storage=%+v", cfg.Storage.S3)
	}
	if cfg.Custom != "custom-value" {
		t.Fatalf("custom=%q", cfg.Custom)
	}
	if cfg.Ignored != "" {
		t.Fatalf("ignored=%q want skipped", cfg.Ignored)
	}
}

func TestLoadEnvEmptyIgnored(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "addr: \":8081\"\n")
	opts := testOptions(path)
	opts.LookupEnv = envLookup(map[string]string{"APP_ADDR": "   "})
	cfg := loadConfig(t, newLoader(t, opts))
	if cfg.Addr != ":8081" {
		t.Fatalf("addr=%q want file value", cfg.Addr)
	}
}

func TestLoadEnvInvalidValue(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "duration", key: "APP_SHUTDOWN_TIMEOUT", value: "not-a-duration"},
		{name: "float", key: "APP_RATIO", value: "abc"},
		{name: "bool", key: "APP_ENABLED", value: "maybe"},
	}
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := testOptions(path)
			opts.LookupEnv = envLookup(map[string]string{tt.key: tt.value})
			_, err := newLoader(t, opts).Load()
			if !errors.Is(err, config.ErrEnvOverride) {
				t.Fatalf("err=%v want ErrEnvOverride", err)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("err=%v should name variable", err)
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Fatalf("err=%v leaks value", err)
			}
		})
	}
}

func TestLoadEnvIntegerBounds(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "")

	type bounded struct {
		Count int8 `yaml:"count"`
	}
	opts := config.Options[bounded]{
		Files:     []string{path},
		EnvPrefix: "APP",
		LookupEnv: envLookup(map[string]string{"APP_COUNT": "200"}),
	}
	loader, err := config.NewLoader(opts)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	if _, err := loader.Load(); !errors.Is(err, config.ErrEnvOverride) {
		t.Fatalf("err=%v want ErrEnvOverride", err)
	}
}

func TestLoadCustomDecoder(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "ignored by custom decoder")
	opts := testOptions(path)
	opts.Defaults = nil
	opts.Validate = nil
	opts.Decode = func(_ []byte, dst *testConfig) error {
		dst.Addr = ":7000"
		return nil
	}
	cfg := loadConfig(t, newLoader(t, opts))
	if cfg.Addr != ":7000" {
		t.Fatalf("addr=%q", cfg.Addr)
	}
}

func TestNewLoaderRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opts config.Options[testConfig]
	}{
		{name: "no sources", opts: config.Options[testConfig]{}},
		{name: "blank file path", opts: config.Options[testConfig]{Files: []string{" "}}},
		{name: "duplicate files", opts: config.Options[testConfig]{Files: []string{"a.yaml", "a.yaml"}}},
		{name: "blank prefix only", opts: config.Options[testConfig]{EnvPrefix: "!!!"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := config.NewLoader(tt.opts); !errors.Is(err, config.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNewLoaderDoesNotMutateOptions(t *testing.T) {
	files := []string{"  a.yaml  "}
	if _, err := config.NewLoader(config.Options[testConfig]{Files: files}); err != nil {
		t.Fatalf("new loader: %v", err)
	}
	if files[0] != "  a.yaml  " {
		t.Fatalf("caller slice mutated: %q", files[0])
	}
}

func TestNilLoaderLoad(t *testing.T) {
	var loader *config.Loader[testConfig]
	if _, err := loader.Load(); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}

func TestLoadEnvOnly(t *testing.T) {
	opts := testOptions()
	opts.Files = nil
	opts.LookupEnv = envLookup(map[string]string{"APP_ADDR": ":6060"})
	cfg := loadConfig(t, newLoader(t, opts))
	if cfg.Addr != ":6060" {
		t.Fatalf("addr=%q", cfg.Addr)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadPrefixNormalized(t *testing.T) {
	opts := testOptions()
	opts.Files = nil
	opts.EnvPrefix = "app-frame"
	opts.LookupEnv = envLookup(map[string]string{"APP_FRAME_ADDR": ":5050"})
	cfg := loadConfig(t, newLoader(t, opts))
	if cfg.Addr != ":5050" {
		t.Fatalf("addr=%q", cfg.Addr)
	}
}

func TestLoadTextUnmarshaler(t *testing.T) {
	type scheduled struct {
		Start time.Time `yaml:"start"`
	}
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "")
	opts := config.Options[scheduled]{
		Files:     []string{path},
		EnvPrefix: "APP",
		LookupEnv: envLookup(map[string]string{"APP_START": "2026-10-20T10:00:00Z"}),
	}
	loader, err := config.NewLoader(opts)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	if !cfg.Start.Equal(want) {
		t.Fatalf("start=%s want %s", cfg.Start, want)
	}
}

func TestLoadEnvExplicitPointerFlag(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "")
	opts := testOptions(path)
	opts.LookupEnv = envLookup(map[string]string{"APP_ENABLED": "false"})
	cfg := loadConfig(t, newLoader(t, opts))
	if cfg.Enabled == nil || *cfg.Enabled {
		t.Fatalf("enabled=%v want explicit false", cfg.Enabled)
	}
}

// selfValidating 覆盖 Validatable 自动校验路径。
type selfValidating struct {
	Addr string `yaml:"addr"`
}

// Validate 复用测试哨兵，便于断言错误链。
func (c selfValidating) Validate() error {
	if c.Addr == "" {
		return errAddrRequired
	}
	return nil
}

// normalizeProbe 用于断言 Normalize 的执行时机与可见状态。
type normalizeProbe struct {
	Channel string `yaml:"channel"`
	Derived string `yaml:"derived"`
}

func TestLoadNormalizeRunsAfterEnv(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "channel: file\n")
	loader, err := config.NewLoader(config.Options[normalizeProbe]{
		Files:     []string{path},
		EnvPrefix: "APP",
		LookupEnv: envLookup(map[string]string{"APP_CHANNEL": "env"}),
		Normalize: func(c *normalizeProbe) {
			if c.Channel != "env" {
				t.Errorf("normalize saw channel=%q, want env override applied", c.Channel)
			}
			c.Derived = c.Channel + "-derived"
		},
		Validate: func(c *normalizeProbe) error {
			if c.Derived != "env-derived" {
				t.Errorf("validate saw derived=%q", c.Derived)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Derived != "env-derived" {
		t.Fatalf("derived=%q", cfg.Derived)
	}
}

func TestLoadAutoValidation(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.yaml", "")
	loader, err := config.NewLoader(config.Options[selfValidating]{Files: []string{path}})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	if _, err := loader.Load(); !errors.Is(err, errAddrRequired) {
		t.Fatalf("err=%v want validation sentinel", err)
	}

	override, err := config.NewLoader(config.Options[selfValidating]{
		Files:    []string{path},
		Validate: func(*selfValidating) error { return nil },
	})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	if _, err := override.Load(); err != nil {
		t.Fatalf("explicit Validate should override Validatable: %v", err)
	}
}
