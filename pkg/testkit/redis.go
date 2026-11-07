package testkit

import (
	"context"
	"strconv"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/redis"
)

// Redis 打开测试 Redis 客户端；未配置 TEST_REDIS_ADDR 时跳过用例。
//
// 所有 key 自动带 testkit 前缀，避免与并行用例或其他测试数据的键冲突。
func Redis(t *testing.T) *redis.Client {
	t.Helper()
	cfg := redis.Config{
		Addr:      RequireEnv(t, EnvRedisAddr),
		Password:  envValue(EnvRedisPassword),
		KeyPrefix: "testkit",
	}
	if raw := envValue(EnvRedisDB); raw != "" {
		dbNum, err := strconv.Atoi(raw)
		if err != nil || dbNum < 0 {
			t.Fatalf("%s must be a non-negative integer, got %q", EnvRedisDB, raw)
		}
		cfg.DB = dbNum
	}

	client, err := redis.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open test redis: %v", err)
	}
	t.Cleanup(func() {
		// 关闭失败仅影响连接池回收，不覆盖测试断言结果。
		_ = client.Close()
	})
	return client
}
