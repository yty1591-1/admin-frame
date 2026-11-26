package platform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
	"github.com/admin-frame/admin-frame/internal/security"
)

// testJWTSecret 返回长度恰为 32 字节的 HMAC 测试密钥。
//
// 为什么不用字面量：pkg/jwt 要求 HS256 密钥 ≥32 字节，而任何 32 位高熵字面量
// 都会被密钥扫描（gitleaks generic-api-key）识别为疑似真实凭据，导致 CI
// `make secrets` 误报。这里用重复字符构造，既满足长度约束，也表达「这是假密钥」。
//
// fill 取不同的值可得到互不相等的密钥（用于轮换场景）。
func testJWTSecret(fill byte) string {
	return strings.Repeat(string(fill), 32)
}

// writeConfig 写入临时配置文件并返回路径。
//
// 平台级测试（bootstrap / middleware）仍需要它；配置包内部测试另有同名副本。
// content 必须显式给出 env（local/dev/test/staging/prod）：空 env 会被
// validateEnv 拒绝，装配会在加载配置阶段就失败，测不到目标路径。
func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// iamBaseConfig 返回可直接 Validate 的最小 IAM 配置（字段已按 applyDefaults 填充）。
func iamBaseConfig() *platformconfig.Config {
	return &platformconfig.Config{
		Env:     "local",
		Service: "admin-api",
		Channel: "admin",
		Addr:    ":8081",
		Auth:    platformconfig.AuthConfig{Mode: platformconfig.AuthModeDemo, ExpectedChannel: "admin"},
		Log:     platformconfig.LogConfig{Level: "info", Format: "json"},
		DB:      platformconfig.DBConfig{Driver: "memory"},
		Storage: platformstorage.Config{Driver: "memory"},
		HTTP:    platformconfig.HTTPConfig{Timeout: 30 * time.Second},
	}
}

// prodJWTConfig 返回可通过生产校验的最小 JWT 配置（仅测试用假密钥）。
func prodJWTConfig() platformconfig.AuthConfig {
	return platformconfig.AuthConfig{
		Mode:            platformconfig.AuthModeJWT,
		ExpectedChannel: "admin",
		JWT: security.JWTConfig{
			Alg:      "HS256",
			Issuer:   "https://iam.example.com",
			Audience: []string{"admin"},
			Secret:   "0123456789abcdef0123456789abcdef",
		},
	}
}

// prodIDConfig 返回满足生产必填项的最小 ID 配置。
func prodIDConfig() platformconfig.IDConfig {
	nodeID := int64(7)
	return platformconfig.IDConfig{NodeID: &nodeID}
}

// prodIdempotencyConfig 补齐生产 A/B 端强制项：幂等开启且缺键即拒绝。
func prodIdempotencyConfig(cfg *platformconfig.Config) *platformconfig.Config {
	cfg.Idempotency = platformconfig.IdempotencyConfig{
		Enabled:      true,
		Driver:       platformconfig.IdempotencyDriverRedis,
		Header:       "Idempotency-Key",
		TTL:          24 * time.Hour,
		Required:     true,
		MaxBodyBytes: 1 << 20,
	}
	return cfg
}

// prodBaseConfig 返回满足生产校验基线的最小配置。
func prodBaseConfig() *platformconfig.Config {
	cfg := iamBaseConfig()
	cfg.Env = "prod"
	cfg.Auth = prodJWTConfig()
	cfg.Storage.Driver = "s3"
	cfg.DB = platformconfig.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	cfg.Redis = platformconfig.RedisConfig{Addr: "127.0.0.1:6379"}
	cfg.Async.Enabled = true
	cfg.ID = prodIDConfig()
	prodIdempotencyConfig(cfg)
	return cfg
}
