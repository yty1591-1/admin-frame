package config_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/config"
)

type envNaming struct {
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	DB              struct {
		DSN string `yaml:"dsn"`
	} `yaml:"db"`
	Storage struct {
		S3 struct {
			AccessKey string `yaml:"access_key"`
		} `yaml:"s3"`
	} `yaml:"storage"`
	HTTPClient struct {
		URL string `yaml:"url"`
	} `yaml:"httpclient"`
}

func TestBindEnvNaming(t *testing.T) {
	var cfg envNaming
	lookup := envLookup(map[string]string{
		"APP_SHUTDOWN_TIMEOUT":      "15s",
		"APP_DB_DSN":                "postgres://db",
		"APP_STORAGE_S3_ACCESS_KEY": "key",
		"APP_HTTPCLIENT_URL":        "https://example.com",
	})
	if err := config.BindEnv(&cfg, "APP", lookup); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("shutdown_timeout=%s", cfg.ShutdownTimeout)
	}
	if cfg.DB.DSN != "postgres://db" {
		t.Fatalf("db.dsn=%q", cfg.DB.DSN)
	}
	if cfg.Storage.S3.AccessKey != "key" {
		t.Fatalf("storage.s3.access_key=%q", cfg.Storage.S3.AccessKey)
	}
	if cfg.HTTPClient.URL != "https://example.com" {
		t.Fatalf("httpclient.url=%q", cfg.HTTPClient.URL)
	}
}

func TestBindEnvSnakeCase(t *testing.T) {
	type naming struct {
		DB          string
		DSN         string
		S3Bucket    string
		AccessKeyID string
		HTTPPort    int
	}
	var cfg naming
	lookup := envLookup(map[string]string{
		"APP_DB":            "db",
		"APP_DSN":           "dsn",
		"APP_S3_BUCKET":     "bucket",
		"APP_ACCESS_KEY_ID": "id",
		"APP_HTTP_PORT":     "8080",
	})
	if err := config.BindEnv(&cfg, "APP", lookup); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if cfg.DB != "db" || cfg.DSN != "dsn" || cfg.S3Bucket != "bucket" || cfg.AccessKeyID != "id" || cfg.HTTPPort != 8080 {
		t.Fatalf("unexpected naming result: %+v", cfg)
	}
}

func TestBindEnvEmbeddedInline(t *testing.T) {
	type embedded struct {
		Level string `yaml:"level"`
	}
	type outer struct {
		embedded `yaml:",inline"`
		Name     string `yaml:"name"`
	}
	var cfg outer
	lookup := envLookup(map[string]string{"APP_LEVEL": "debug", "APP_NAME": "svc"})
	if err := config.BindEnv(&cfg, "APP", lookup); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if cfg.Level != "debug" || cfg.Name != "svc" {
		t.Fatalf("embedded binding failed: %+v", cfg)
	}
}

func TestBindEnvPointerStructLazy(t *testing.T) {
	type optional struct {
		Enabled bool `yaml:"enabled"`
	}
	type outer struct {
		Optional *optional `yaml:"optional"`
	}
	var unset outer
	if err := config.BindEnv(&unset, "APP", envLookup(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if unset.Optional != nil {
		t.Fatalf("optional should stay nil when no env provided")
	}

	var set outer
	if err := config.BindEnv(&set, "APP", envLookup(map[string]string{"APP_OPTIONAL_ENABLED": "true"})); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if set.Optional == nil || !set.Optional.Enabled {
		t.Fatalf("optional not allocated: %+v", set.Optional)
	}
}

func TestBindEnvPreservesExistingPointerSection(t *testing.T) {
	type optional struct {
		Enabled bool   `yaml:"enabled"`
		Mode    string `yaml:"mode"`
	}
	type outer struct {
		Optional *optional `yaml:"optional"`
	}
	cfg := outer{Optional: &optional{Mode: "file"}}
	if err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"APP_OPTIONAL_ENABLED": "true"})); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if cfg.Optional == nil || !cfg.Optional.Enabled || cfg.Optional.Mode != "file" {
		t.Fatalf("merge lost file value: %+v", cfg.Optional)
	}
}

func TestBindEnvSkipsUnsupportedImplicit(t *testing.T) {
	type withMap struct {
		Meta map[string]string `yaml:"meta"`
		Any  any               `yaml:"any"`
		Name string            `yaml:"name"`
	}
	var cfg withMap
	if err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"APP_NAME": "ok"})); err != nil {
		t.Fatalf("unsupported implicit fields should be skipped: %v", err)
	}
	if cfg.Name != "ok" {
		t.Fatalf("name=%q", cfg.Name)
	}
}

func TestBindEnvRejectsUnsupportedExplicit(t *testing.T) {
	type withMap struct {
		Meta map[string]string `yaml:"meta" env:"APP_META"`
	}
	var cfg withMap
	err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"APP_META": "x"}))
	if !errors.Is(err, config.ErrUnsupportedField) {
		t.Fatalf("err=%v want ErrUnsupportedField", err)
	}
}

func TestBindEnvRejectsNestedEnvTag(t *testing.T) {
	type nested struct {
		Name string `yaml:"name"`
	}
	type outer struct {
		Nested nested `yaml:"nested" env:"APP_NESTED"`
	}
	var cfg outer
	err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"APP_NESTED_NAME": "x"}))
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "yaml tag") {
		t.Fatalf("err=%v should suggest yaml tag", err)
	}
}

func TestBindEnvRejectsMultilevelPointer(t *testing.T) {
	type outer struct {
		Nested **struct {
			Name string `yaml:"name"`
		} `yaml:"nested"`
	}
	var cfg outer
	if err := config.BindEnv(&cfg, "APP", envLookup(nil)); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}

func TestBindEnvTargetValidation(t *testing.T) {
	var scalar int
	tests := []struct {
		name   string
		target any
	}{
		{name: "nil", target: nil},
		{name: "non pointer", target: testConfig{}},
		{name: "nil pointer", target: (*testConfig)(nil)},
		{name: "pointer to scalar", target: &scalar},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := config.BindEnv(tt.target, "APP", envLookup(nil)); !errors.Is(err, config.ErrInvalidConfig) {
				t.Fatalf("err=%v want ErrInvalidConfig", err)
			}
		})
	}
}

func TestBindEnvExplicitAbsoluteIgnoresPrefix(t *testing.T) {
	type outer struct {
		Value string `yaml:"value" env:"ABSOLUTE_NAME"`
	}
	var cfg outer
	if err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"ABSOLUTE_NAME": "v"})); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if cfg.Value != "v" {
		t.Fatalf("value=%q", cfg.Value)
	}
}

func TestBindEnvNilLookupUsesOS(t *testing.T) {
	type outer struct {
		Value string `yaml:"value" env:"TEST_ONLY_ENV_VALUE"`
	}
	t.Setenv("TEST_ONLY_ENV_VALUE", "from-os")
	var cfg outer
	if err := config.BindEnv(&cfg, "APP", nil); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if cfg.Value != "from-os" {
		t.Fatalf("value=%q", cfg.Value)
	}
}

func TestBindEnvUintAndTextErrors(t *testing.T) {
	type outer struct {
		Size  uint32    `yaml:"size"`
		Start time.Time `yaml:"start"`
	}
	var cfg outer
	if err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"APP_SIZE": "-1"})); !errors.Is(err, config.ErrEnvOverride) {
		t.Fatalf("negative uint err=%v", err)
	}
	if err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"APP_START": "not-a-time"})); !errors.Is(err, config.ErrEnvOverride) {
		t.Fatalf("bad time err=%v", err)
	}
	if err := config.BindEnv(&cfg, "APP", envLookup(map[string]string{"APP_START": "2026-10-20T10:00:00Z"})); err != nil {
		t.Fatalf("valid time: %v", err)
	}
}

func TestBindEnvRejectsOversizedKey(t *testing.T) {
	field := reflect.StructField{
		Name: "Value",
		Type: reflect.TypeOf(""),
		Tag:  reflect.StructTag(`env:"` + strings.Repeat("A", 200) + `"`),
	}
	target := reflect.New(reflect.StructOf([]reflect.StructField{field})).Interface()
	if err := config.BindEnv(target, "APP", envLookup(nil)); !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("err=%v want ErrInvalidConfig", err)
	}
}
