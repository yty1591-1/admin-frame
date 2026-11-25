package config

import (
	"log/slog"
	"strings"
)

// RedisConfig 是共享缓存 / 锁的 Redis 连接参数（可选）。
//
// Addr 为空表示本进程不启用 Redis；IAM `cache.driver=redis` 时必须配置。
// 口令优先配置中心密钥子域（redis.password）；部署 env 仅作迁移期兜底。
type RedisConfig struct {
	Addr      string `yaml:"addr"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	DB        int    `yaml:"db"`
	KeyPrefix string `yaml:"key_prefix"`
}

// Enabled 报告是否配置了 Redis。
func (r RedisConfig) Enabled() bool { return strings.TrimSpace(r.Addr) != "" }

// LogValue 实现 slog.LogValuer：口令只输出占位符（SEC-05）。
func (r RedisConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", r.Addr),
		slog.String("username", r.Username),
		slog.String("password", maskIfSet(r.Password)),
		slog.Int("db", r.DB),
		slog.String("key_prefix", r.KeyPrefix),
	)
}
