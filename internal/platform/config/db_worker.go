package config

import (
	"log/slog"
	"strings"
	"time"
)

// DBConfig 数据源驱动、连接串与连接池参数。
type DBConfig struct {
	Driver string `yaml:"driver"` // memory | mysql | postgres
	DSN    string `yaml:"dsn"`

	// MaxOpenConns 单进程连接池上限；0 沿用 pkg/db 默认值（25）。
	//
	// 该值即 db_pool_max_open_connections 指标与 DBPoolExhaustion 告警的分母，
	// 容量规划按「副本数 × max_open_conns ≤ MySQL max_connections × 0.8」反推；
	// 调小后告警更容易命中，故障注入演练用它缩短触发时间。
	MaxOpenConns int `yaml:"max_open_conns"`
	// MaxIdleConns 空闲连接数；0 沿用默认值（5），显式设置时不得超过 MaxOpenConns。
	MaxIdleConns int `yaml:"max_idle_conns"`
	// ConnMaxLifetime 连接最长存活时间；0 沿用默认值（30m）。
	//
	// 必须小于 MySQL wait_timeout，否则拿到的是已被服务端回收的连接（database/sql 会重试，
	// 但会累积 1 次无效往返）。
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

// LogValue 实现 slog.LogValuer：DSN 只保留 scheme，避免凭据进日志（SEC-05）。
func (c DBConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("driver", c.Driver),
		slog.String("dsn", redactDSN(c.DSN)),
		slog.Int("max_open_conns", c.MaxOpenConns),
		slog.Int("max_idle_conns", c.MaxIdleConns),
		slog.Duration("conn_max_lifetime", c.ConnMaxLifetime),
	)
}

// WorkerConfig 后台 worker 轮询等参数。
type WorkerConfig struct {
	PollInterval time.Duration `yaml:"poll_interval"`
}

// LogValue 实现 slog.LogValuer：输出可安全落盘的配置快照（SEC-05）。
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("service", c.Service),
		slog.String("channel", c.Channel),
		slog.String("addr", c.Addr),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
		slog.Group("http",
			slog.Duration("read_timeout", c.HTTP.ReadTimeout),
			slog.Duration("write_timeout", c.HTTP.WriteTimeout),
			slog.Duration("idle_timeout", c.HTTP.IdleTimeout),
			slog.Duration("timeout", c.HTTP.Timeout),
		),
		slog.Group("log",
			slog.String("level", c.Log.Level),
			slog.String("format", c.Log.Format),
		),
		slog.Group("auth",
			slog.String("mode", c.Auth.effectiveMode(c.Env)),
			slog.String("expected_channel", c.Auth.ExpectedChannel),
			slog.Group("jwt",
				slog.String("alg", c.Auth.JWT.Alg),
				slog.String("issuer", c.Auth.JWT.Issuer),
				slog.String("secret", maskIfSet(c.Auth.JWT.Secret)),
				slog.Int("rotating_secrets", len(c.Auth.JWT.Secrets)),
			),
		),
		slog.Any("db", c.DB),
		slog.Any("redis", c.Redis),
		slog.Any("storage", c.Storage),
		slog.Group("worker", slog.Duration("poll_interval", c.Worker.PollInterval)),
		slog.Group("tracing",
			slog.String("exporter", c.Tracing.Exporter),
			slog.Bool("enabled", c.Tracing.IsEnabled()),
		),
		slog.Group("id",
			slog.String("node_id_source", c.ID.NodeIDSource),
			slog.Any("node_id", c.ID.NodeID),
		),
		slog.Group("idempotency",
			slog.Bool("enabled", c.Idempotency.Enabled),
			slog.String("driver", c.Idempotency.Driver),
			slog.Duration("ttl", c.Idempotency.TTL),
			slog.Bool("required", c.Idempotency.Required),
		),
		slog.Group("rate_limit",
			slog.Bool("enabled", c.RateLimit.Enabled),
			slog.String("driver", c.RateLimit.Driver),
			slog.String("key", c.RateLimit.Key),
			slog.String("failure_mode", c.RateLimit.FailureMode),
			slog.Float64("rps", c.RateLimit.RPS),
			slog.Int("burst", c.RateLimit.Burst),
		),
		slog.Group("async",
			slog.Bool("enabled", c.Async.Enabled),
			slog.String("driver", c.Async.Driver),
			slog.Bool("publish_in_api", c.Async.Outbox.PublishInAPI),
			slog.Duration("poll_interval", c.Async.Outbox.PollInterval),
			slog.Int("batch_size", c.Async.Outbox.BatchSize),
			slog.Duration("lease", c.Async.Outbox.Lease),
			slog.Int("outbox_concurrency", c.Async.Outbox.Concurrency),
			slog.Int("consumer_concurrency", c.Async.Consumer.Concurrency),
			slog.String("retry_backoff", c.Async.Consumer.RetryBackoff),
		),
		slog.Group("iam",
			slog.String("mode", normalizeIAMMode(c.IAM.Mode)),
			slog.Duration("load_timeout", c.IAM.LoadTimeout),
			slog.Group("cache",
				slog.String("driver", c.IAM.Cache.Driver),
				slog.Duration("l1_ttl", c.IAM.Cache.L1TTL),
				slog.Duration("l2_ttl", c.IAM.Cache.L2TTL),
				slog.Duration("negative_ttl", c.IAM.Cache.NegativeTTL),
			),
			slog.Bool("allow_wildcard", c.IAM.Security.AllowWildcard),
		),
		slog.Group("identity",
			slog.Bool("enabled", c.Identity.Enabled),
			slog.Duration("access_ttl", c.Identity.AccessTTL),
			slog.Duration("refresh_ttl", c.Identity.RefreshTTL),
			slog.Group("revocation",
				slog.String("driver", c.Identity.Revocation.Driver),
				slog.String("failure_mode", c.Identity.Revocation.FailureMode),
				slog.Duration("state_ttl", c.Identity.Revocation.StateTTL),
			),
			slog.Group("mfa",
				slog.Bool("admin_required", c.Identity.MFA.AdminRequired),
				slog.String("issuer", c.Identity.MFA.Issuer),
				slog.Bool("secret_key_configured", strings.TrimSpace(c.Identity.MFA.SecretKey) != ""),
			),
			slog.Group("sms",
				slog.Bool("enabled", c.Identity.SMS.Enabled),
				slog.String("driver", c.Identity.SMS.Driver),
				slog.String("template_id", c.Identity.SMS.TemplateID),
				slog.Duration("code_ttl", c.Identity.SMS.CodeTTL),
				slog.Int("max_attempts", c.Identity.SMS.MaxAttempts),
			),
			slog.Group("oauth",
				slog.Bool("auto_provision", c.Identity.OAuth.AutoProvision),
				slog.Int("providers", len(c.Identity.OAuth.Providers)),
				slog.Any("provider_names", oauthProviderNames(c.Identity.OAuth.Providers)),
			),
			slog.Group("saml",
				slog.Bool("enabled", c.Identity.SAML.Enabled),
				slog.Bool("auto_provision", c.Identity.SAML.AutoProvision),
				slog.Int("providers", len(c.Identity.SAML.Providers)),
				slog.Bool("private_key_configured", strings.TrimSpace(c.Identity.SAML.PrivateKey) != ""),
				slog.Bool("next_key_configured", strings.TrimSpace(c.Identity.SAML.CertificateNextPEM) != ""),
			),
		),
	)
}
