package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/pkg/config"
)

// testConfig 覆盖 pkg/config 支持的主要字段形态。
type testConfig struct {
	Env             string            `yaml:"env"`
	Addr            string            `yaml:"addr"`
	ShutdownTimeout time.Duration     `yaml:"shutdown_timeout"`
	Ratio           *float64          `yaml:"ratio"`
	Enabled         *bool             `yaml:"enabled"`
	Tags            []string          `yaml:"tags"`
	Log             logConfig         `yaml:"log"`
	Auth            *authConfig       `yaml:"auth"`
	Storage         storageConfig     `yaml:"storage"`
	Meta            map[string]string `yaml:"meta"`
	Custom          string            `yaml:"custom" env:"TEST_ONLY_CUSTOM"`
	Ignored         string            `yaml:"ignored" env:"-"`
}

type logConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type authConfig struct {
	Mode string `yaml:"mode"`
}

type storageConfig struct {
	S3 s3Config `yaml:"s3"`
}

type s3Config struct {
	Endpoint       string `yaml:"endpoint"`
	ForcePathStyle bool   `yaml:"force_path_style"`
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func testOptions(files ...string) config.Options[testConfig] {
	return config.Options[testConfig]{
		Files:     files,
		EnvPrefix: "APP",
		Defaults: func(c *testConfig) {
			c.Addr = ":8080"
			c.Log.Level = "info"
		},
		Validate: func(c *testConfig) error {
			if c.Addr == "" {
				return errAddrRequired
			}
			return nil
		},
	}
}

// errAddrRequired 是校验失败的稳定哨兵，便于断言错误链。
var errAddrRequired = os.ErrInvalid

func newLoader(t *testing.T, opts config.Options[testConfig]) *config.Loader[testConfig] {
	t.Helper()
	loader, err := config.NewLoader(opts)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	return loader
}

func loadConfig(t *testing.T, loader *config.Loader[testConfig]) *testConfig {
	t.Helper()
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// waitEvent 等待首个满足条件的重载事件；超时即失败。
func waitEvent(t *testing.T, events <-chan config.Event[testConfig], timeout time.Duration,
	accept func(config.Event[testConfig]) bool) config.Event[testConfig] {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			if accept(event) {
				return event
			}
		case <-deadline:
			t.Fatalf("expected event not observed within %s", timeout)
		}
	}
}
