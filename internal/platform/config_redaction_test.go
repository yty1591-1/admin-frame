package platform_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/internal/platform"
	"github.com/admin-frame/admin-frame/pkg/storage"
)

// TestConfigLogValueRedactsSecrets 验证日志快照不包含任何密钥/DSN（SEC-05）。
func TestConfigLogValueRedactsSecrets(t *testing.T) {
	const (
		dsnSecret    = "postgres://user:super-secret@db:5432/app" //nolint:gosec // 测试用假 DSN，仅用于验证脱敏输出。
		storageAK    = "storage-access-key"
		storageSK    = "storage-secret-key"
		sessionToken = "session-token"
	)
	jwtSecret := testJWTSecret('j')
	cfg := &platform.Config{
		Env:     "prod",
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth: platform.AuthConfig{
			Mode: "jwt",
		},
		DB: platform.DBConfig{Driver: "postgres", DSN: dsnSecret},
	}
	cfg.Auth.JWT.Secret = jwtSecret
	cfg.Storage.S3.AccessKey = storageAK
	cfg.Storage.S3.SecretKey = storageSK
	cfg.Storage.S3.SessionToken = sessionToken

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("config", "config", cfg)

	out := buf.String()
	for _, secret := range []string{dsnSecret, jwtSecret, storageAK, storageSK, sessionToken, "super-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log output leaks secret %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "postgres://***") {
		t.Fatalf("dsn scheme should be kept: %s", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("expected masked placeholder in %s", out)
	}
}

// TestValidateAggregatesErrors 验证校验错误一次性聚合而非遇到首个错误就返回。
func TestValidateAggregatesErrors(t *testing.T) {
	cfg := &platform.Config{
		Service: "",
		Channel: "not-a-channel",
		Addr:    "",
		Auth:    platform.AuthConfig{ExpectedChannel: "c"},
		Log:     platform.LogConfig{Level: "verbose", Format: "yaml"},
		DB:      platform.DBConfig{Driver: "oracle"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	lines := strings.Count(err.Error(), "\n") + 1
	if lines < 5 {
		t.Fatalf("expected multiple aggregated errors, got %d lines: %v", lines, err)
	}
	for _, want := range []string{"service is required", "unsupported channel", "unsupported log.level", "unsupported log.format", "unsupported db.driver"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

// TestValidateLogFormat 验证非法格式与 prod+text 均在启动前拦截。
func TestValidateLogFormat(t *testing.T) {
	base := func() *platform.Config {
		return &platform.Config{
			Service: "c-api",
			Channel: "c",
			Addr:    ":8083",
			Auth:    platform.AuthConfig{ExpectedChannel: "c", Mode: platform.AuthModeDemo},
			Log:     platform.LogConfig{Level: "info", Format: "json"},
			DB:      platform.DBConfig{Driver: "memory"},
		}
	}

	invalid := base()
	invalid.Log.Format = "console"
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported log.format") {
		t.Fatalf("err=%v want unsupported log.format", err)
	}

	prodText := base()
	prodText.Env = "prod"
	prodText.Log.Format = "text"
	if err := prodText.Validate(); err == nil || !strings.Contains(err.Error(), "log.format=text is forbidden") {
		t.Fatalf("err=%v want prod text rejection", err)
	}
}

// TestLoadConfigRejectsInvalidLogFormat 验证非法格式在加载阶段失败而非 panic（CFG-03）。
func TestLoadConfigRejectsInvalidLogFormat(t *testing.T) {
	path := writeConfig(t, "bad-log.yaml", "service: c-api\nchannel: c\nlog:\n  format: xml\n")
	if _, err := platform.LoadConfig(path); err == nil || !strings.Contains(err.Error(), "unsupported log.format") {
		t.Fatalf("err=%v want unsupported log.format", err)
	}
}

// TestValidateHTTPProduction 验证生产必须配置请求超时。
func TestValidateHTTPProduction(t *testing.T) {
	cfg := &platform.Config{
		Env:     "prod",
		Service: "c-api",
		Channel: "c",
		Addr:    ":8083",
		Auth:    platform.AuthConfig{ExpectedChannel: "c", Mode: platform.AuthModeDemo},
		Log:     platform.LogConfig{Level: "info", Format: "json"},
		DB:      platform.DBConfig{Driver: "memory"},
		HTTP:    platform.HTTPConfig{Timeout: 0},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "http.timeout") {
		t.Fatalf("err=%v want http.timeout rejection", err)
	}
}

// TestValidateAuthModes 验证 demo/prod 互斥与 jwt 缺配置失败。
func TestValidateAuthModes(t *testing.T) {
	base := func() *platform.Config {
		return &platform.Config{
			Env:     "prod",
			Service: "c-api",
			Channel: "c",
			Addr:    ":8083",
			Auth:    platform.AuthConfig{ExpectedChannel: "c", Mode: platform.AuthModeDemo},
			Log:     platform.LogConfig{Level: "info", Format: "json"},
			DB:      platform.DBConfig{Driver: "memory"},
			HTTP:    platform.HTTPConfig{Timeout: 30e9},
		}
	}
	if err := base().Validate(); err == nil || !strings.Contains(err.Error(), "auth.mode=demo is forbidden") {
		t.Fatalf("err=%v want demo prod rejection", err)
	}

	jwt := base()
	jwt.Auth.Mode = platform.AuthModeJWT
	err := jwt.Validate()
	if err == nil || !strings.Contains(err.Error(), "auth.jwt.issuer is required") {
		t.Fatalf("err=%v want jwt issuer rejection", err)
	}
}

// TestValidateStorageNestedBuckets 验证命名桶内不允许再次声明 buckets（防止配置歧义）。
func TestValidateStorageNestedBuckets(t *testing.T) {
	cfg := platform.StorageConfig{
		Driver: "memory",
		Buckets: map[string]platform.BucketConfig{
			"public": {
				Driver: "memory",
				Buckets: map[string]platform.BucketConfig{
					"nested": {Driver: "memory"},
				},
			},
		},
	}
	if err := platform.ValidateStorage("local", cfg); err == nil || !strings.Contains(err.Error(), "nested buckets") {
		t.Fatalf("err=%v want nested buckets rejection", err)
	}
}

// TestStorageConfigMapping 校验 StorageConfig → pkg/storage.Config 的字段映射完整。
func TestStorageConfigMapping(t *testing.T) {
	// 字段数量漂移守卫：storage.Config 增删字段时必须同步更新映射与断言（PLAT-09）。
	const wantStorageFields = 10
	if n := reflect.TypeOf(storage.Config{}).NumField(); n != wantStorageFields {
		t.Fatalf("storage.Config has %d fields (want %d); update StorageConfig mapping and this test", n, wantStorageFields)
	}
	srcType := reflect.TypeOf(platform.StorageConfig{})
	for i := 0; i < reflect.TypeOf(storage.Config{}).NumField(); i++ {
		name := reflect.TypeOf(storage.Config{}).Field(i).Name
		if _, ok := srcType.FieldByName(name); !ok {
			t.Fatalf("platform.StorageConfig missing field %q required by storage.Config", name)
		}
	}

	cfg := platform.StorageConfig{
		Driver:         "s3",
		KeyPrefix:      "admin/",
		MaxObjectBytes: 1024,
		PresignPutTTL:  60e9,
		PresignGetTTL:  120e9,
	}
	cfg.S3.Endpoint = "http://127.0.0.1:9000"
	cfg.S3.Bucket = "bucket"
	cfg.OSS.Endpoint = "oss-endpoint"
	cfg.COS.Endpoint = "cos-endpoint"
	cfg.OBS.Endpoint = "obs-endpoint"
	cfg.Local.Root = "./data"

	got := cfg.ToStorageConfig()
	if got.Driver != cfg.Driver || got.KeyPrefix != cfg.KeyPrefix ||
		got.MaxObjectBytes != cfg.MaxObjectBytes ||
		got.PresignPutTTL != cfg.PresignPutTTL || got.PresignGetTTL != cfg.PresignGetTTL {
		t.Fatalf("scalar fields not mapped: %+v", got)
	}
	if got.S3.Endpoint != cfg.S3.Endpoint || got.S3.Bucket != cfg.S3.Bucket {
		t.Fatalf("s3 not mapped: %+v", got.S3)
	}
	if got.OSS.Endpoint != cfg.OSS.Endpoint || got.COS.Endpoint != cfg.COS.Endpoint || got.OBS.Endpoint != cfg.OBS.Endpoint {
		t.Fatalf("cloud endpoints not mapped: %+v", got)
	}
	if got.Local.Root != cfg.Local.Root {
		t.Fatalf("local not mapped: %+v", got.Local)
	}
}

// TestLoadConfigExampleFiles 逐一加载仓库内示例配置，保证示例与结构体同步。
//
// 用 yaml 解析而非 LoadConfig，避免示例中的 prod 占位值触发环境校验。
func TestLoadConfigExampleFiles(t *testing.T) {
	files, err := filepath.Glob("../../configs/*.yaml.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no example configs found")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file) // #nosec G304 -- 路径来自仓库内示例配置的 Glob 结果。
			if err != nil {
				t.Fatal(err)
			}
			if len(bytes.TrimSpace(data)) == 0 {
				t.Fatal("empty example")
			}
			// 示例中的 env 均为 local，可直接走 LoadConfig 的严格解码与校验。
			if _, err := platform.LoadConfig(file); err != nil {
				t.Fatalf("load %s: %v", file, err)
			}
		})
	}
}

// TestBuildInfoJSON 保证 /version 字段名稳定（OBS-04）。
func TestBuildInfoJSON(t *testing.T) {
	out, err := json.Marshal(platform.BuildInfo{Service: "c-api", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"service"`, `"version"`, `"commit"`, `"build_time"`} {
		if !strings.Contains(string(out), key) {
			t.Fatalf("missing %s in %s", key, out)
		}
	}
}
