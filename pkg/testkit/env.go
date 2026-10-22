package testkit

import (
	"os"
	"strings"
	"testing"
)

// 集成测试依赖的环境变量约定；未设置时相关用例必须显式跳过。
const (
	// EnvMySQLDSN 指向已完成迁移的测试 MySQL；必须包含 parseTime=true。
	EnvMySQLDSN = "TEST_MYSQL_DSN"
	// EnvRedisAddr 指向测试 Redis 地址（host:port）。
	EnvRedisAddr = "TEST_REDIS_ADDR"
	// EnvRedisPassword 为测试 Redis 口令；默认为空。
	EnvRedisPassword = "TEST_REDIS_PASSWORD"
	// EnvRedisDB 为测试 Redis 逻辑库编号；默认 0。
	EnvRedisDB = "TEST_REDIS_DB"
	// EnvS3Endpoint 指向 MinIO / S3 兼容端点（含 scheme）。
	EnvS3Endpoint = "TEST_S3_ENDPOINT"
	// EnvS3AccessKey 为测试对象存储 AK。
	EnvS3AccessKey = "TEST_S3_ACCESS_KEY"
	// EnvS3SecretKey 为测试对象存储 SK 的环境变量名。
	EnvS3SecretKey = "TEST_S3_SECRET_KEY" // #nosec G101 -- 仅保存环境变量名，不含凭据值。
	// EnvS3Bucket 为测试对象存储 bucket。
	EnvS3Bucket = "TEST_S3_BUCKET"
)

// envValue 返回去空格后的环境变量值；空字符串表示未配置。
func envValue(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

// requireEnv 返回必填环境变量；未配置时跳过当前用例并打印可执行的补救命令。
func requireEnv(t *testing.T, key string) string {
	t.Helper()
	value := envValue(key)
	if value == "" {
		t.Skipf("%s is not set: integration test skipped (run make test-integration or start docker-compose.integration.yml)", key)
	}
	return value
}
