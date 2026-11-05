package platform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
)

// prodBaseConfig 返回满足生产基础校验的最小配置（不含 ID / 幂等 / 限流）。
func prodBaseConfig() *platform.Config {
	cfg := iamBaseConfig()
	cfg.Env = "prod"
	cfg.Auth = prodJWTConfig()
	cfg.Storage.Driver = "s3"
	cfg.DB = platform.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	cfg.IAM.Cache.Driver = platform.IAMCacheDriverRedis
	cfg.Redis = platform.RedisConfig{Addr: "127.0.0.1:6379"}
	cfg.Async.Enabled = true
	cfg.ID = prodIDConfig()
	prodIdempotencyConfig(cfg)
	return cfg
}

func TestValidateIDRules(t *testing.T) {
	missing := prodBaseConfig()
	missing.ID = platform.IDConfig{}
	if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "id.node_id is required") {
		t.Fatalf("err=%v want node_id requirement", err)
	}

	stateful := prodBaseConfig()
	stateful.ID = platform.IDConfig{NodeIDSource: platform.IDSourceStatefulSet}
	if err := stateful.Validate(); err != nil {
		t.Fatalf("statefulset prod config: %v", err)
	}

	conflict := prodBaseConfig()
	conflict.ID = platform.IDConfig{NodeIDSource: platform.IDSourceStatefulSet, NodeID: prodIDConfig().NodeID}
	if err := conflict.Validate(); err == nil || !strings.Contains(err.Error(), "must not be set") {
		t.Fatalf("err=%v want statefulset/node_id conflict", err)
	}

	outOfRange := prodBaseConfig()
	nodeID := int64(1024)
	outOfRange.ID = platform.IDConfig{NodeID: &nodeID}
	if err := outOfRange.Validate(); err == nil || !strings.Contains(err.Error(), "id.node_id must be in [0, 1023]") {
		t.Fatalf("err=%v want node_id range rejection", err)
	}

	registry := prodBaseConfig()
	registry.ID = platform.IDConfig{NodeIDSource: "registry"}
	if err := registry.Validate(); err == nil || !strings.Contains(err.Error(), "registry is not supported") {
		t.Fatalf("err=%v want registry rejection", err)
	}

	worker := prodBaseConfig()
	worker.Service = "worker"
	worker.Channel = "worker"
	worker.Auth = prodJWTConfig()
	worker.Auth.ExpectedChannel = "worker"
	worker.Auth.JWT.Audience = []string{"worker"}
	worker.ID = platform.IDConfig{}
	worker.IAM.Mode = platform.IAMModeOff
	worker.IAM.Cache.Driver = platform.IAMCacheDriverMemory
	worker.IAM.Approval.Enabled = false
	if err := worker.Validate(); err != nil {
		t.Fatalf("worker prod config without node_id: %v", err)
	}
}

func TestValidateIdempotencyRules(t *testing.T) {
	prodMemory := prodBaseConfig()
	prodMemory.Idempotency = platform.IdempotencyConfig{
		Enabled: true, Driver: platform.IdempotencyDriverMemory, Header: "Idempotency-Key", TTL: 24 * time.Hour,
	}
	if err := prodMemory.Validate(); err == nil || !strings.Contains(err.Error(), "idempotency.driver must be redis") {
		t.Fatalf("err=%v want redis requirement", err)
	}

	prodRedis := prodBaseConfig()
	prodRedis.Idempotency = platform.IdempotencyConfig{
		Enabled: true, Driver: platform.IdempotencyDriverRedis, Header: "Idempotency-Key", TTL: 24 * time.Hour,
		Required: true,
	}
	if err := prodRedis.Validate(); err != nil {
		t.Fatalf("prod redis idempotency: %v", err)
	}

	missingRedis := prodBaseConfig()
	missingRedis.Redis = platform.RedisConfig{}
	missingRedis.IAM.Cache.Driver = platform.IAMCacheDriverMemory
	missingRedis.IAM.Mode = platform.IAMModeOff
	missingRedis.Idempotency = platform.IdempotencyConfig{
		Enabled: true, Driver: platform.IdempotencyDriverRedis, Header: "Idempotency-Key", TTL: 24 * time.Hour,
	}
	if err := missingRedis.Validate(); err == nil || !strings.Contains(err.Error(), "redis.addr is required") {
		t.Fatalf("err=%v want redis.addr requirement", err)
	}

	localMemory := iamBaseConfig()
	localMemory.Idempotency = platform.IdempotencyConfig{
		Enabled: true, Driver: platform.IdempotencyDriverMemory, Header: "Idempotency-Key", TTL: 24 * time.Hour,
	}
	if err := localMemory.Validate(); err != nil {
		t.Fatalf("local memory idempotency: %v", err)
	}
}

func TestValidateRateLimitRules(t *testing.T) {
	prodMemory := prodBaseConfig()
	prodMemory.RateLimit = platform.RateLimitConfig{
		Enabled: true, Driver: platform.RateLimitDriverMemory, Key: platform.RateLimitKeyChannel,
		FailureMode: platform.RateLimitFailureOpen, RPS: 100, Burst: 200,
	}
	if err := prodMemory.Validate(); err == nil || !strings.Contains(err.Error(), "rate_limit.driver must be redis") {
		t.Fatalf("err=%v want redis requirement", err)
	}

	prodRedis := prodBaseConfig()
	prodRedis.RateLimit = platform.RateLimitConfig{
		Enabled: true, Driver: platform.RateLimitDriverRedis, Key: platform.RateLimitKeyChannelUser,
		FailureMode: platform.RateLimitFailureClosed, RPS: 100, Burst: 200,
	}
	if err := prodRedis.Validate(); err != nil {
		t.Fatalf("prod redis rate limit: %v", err)
	}

	badKey := iamBaseConfig()
	badKey.RateLimit = platform.RateLimitConfig{
		Enabled: true, Driver: platform.RateLimitDriverMemory, Key: "ip", RPS: 100, Burst: 200,
	}
	if err := badKey.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported rate_limit.key") {
		t.Fatalf("err=%v want key rejection", err)
	}

	badRPS := iamBaseConfig()
	badRPS.RateLimit = platform.RateLimitConfig{
		Enabled: true, Driver: platform.RateLimitDriverMemory, Key: platform.RateLimitKeyChannel,
	}
	if err := badRPS.Validate(); err == nil || !strings.Contains(err.Error(), "rate_limit.rps must be > 0") {
		t.Fatalf("err=%v want rps rejection", err)
	}
}
