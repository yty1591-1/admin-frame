package spec

import (
	"strconv"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// M1-4 键声明盘点：平台启动键与平台热更键、IAM 治理模式。
//
// 规则（CONFIG_STANDARDS CFG-07 / 计划 §6.8）：
//   - 只登记允许进入配置中心的键；密钥、DSN、云厂商凭证与部署身份
//     （env/service/channel/node_id）不登记；
//   - 默认值与代码 applyDefaults 保持一致，有默认值即不标 Required；
//   - 启动期读取的键 Reloadable=false，平台热更白名单内的键为 true；
//   - 业务域（identity/audit/tenant/notification）键由域 owner 后续补齐，
//     本目录不臆造。
const (
	// NamespacePlatform 是进程级平台配置命名空间。
	NamespacePlatform = "platform"
	// NamespaceIAM 是 IAM 治理配置命名空间。
	NamespaceIAM = "iam"

	ownerPlatform = "team-platform"
	ownerIAM      = "team-iam"
)

// Platform 返回已盘点的代码键声明注册表。
func Platform() (*Registry, error) {
	registry := NewRegistry()
	if err := registry.Register(platformSpecs()...); err != nil {
		return nil, err
	}
	if err := registry.Register(iamSpecs()...); err != nil {
		return nil, err
	}
	return registry, nil
}

// platformSpecs 返回平台段键声明（顺序按用途分组，List 会按 key 排序）。
func platformSpecs() []domain.KeySpec {
	return []domain.KeySpec{
		// 进程与入口。
		stringKey("server.addr", ":8080", "HTTP 监听地址", false),
		durationKey("server.shutdown_timeout", "15s", "优雅关停超时", false),

		// HTTP 加固。
		durationKey("http.read_header_timeout", "5s", "读取请求头上限，防御 slowloris", false),
		durationKey("http.read_timeout", "15s", "读取请求含 body 的上限", false),
		durationKey("http.write_timeout", "30s", "写响应上限", false),
		durationKey("http.idle_timeout", "60s", "keep-alive 空闲连接上限", false),
		intKey("http.max_header_bytes", 1<<20, "请求头总字节上限", false, nil, nil),
		durationKey("http.timeout", "30s", "单请求 context deadline", false),
		intKey("http.body_limit_bytes", 32<<20, "请求体字节上限", false, nil, nil),

		// 日志：级别支持热更，格式变更需重启。
		stringKeyWithEnum("log.level", "info", "日志级别", true,
			"debug", "info", "warn", "error"),
		stringKeyWithEnum("log.format", "json", "日志输出格式", false, "json", "text"),

		// 鉴权启动参数；密钥与密钥环不进入配置中心（SEC-01 / M2）。
		stringKeyWithEnum("auth.mode", "", "鉴权模式；空值按环境回退", false, "demo", "jwt"),
		stringKeyWithEnum("auth.expected_channel", "", "期望端别；空值跟随 channel", false,
			"admin", "biz", "c"),
		stringKeyWithEnum("auth.jwt.alg", "", "JWT 签名算法", false, "HS256", "RS256"),
		requiredKey("auth.jwt.issuer", domain.ValueTypeString, "JWT 期望 issuer"),
		requiredKey("auth.jwt.audience", domain.ValueTypeJSON, "JWT 期望 audience 列表（JSON 数组）"),

		// 数据源与缓存：连接串与口令不进配置中心。
		stringKeyWithEnum("db.driver", "memory", "数据源驱动", false, "memory", "mysql", "postgres"),
		intKey("db.max_open_conns", 0, "连接池上限；0 沿用 pkg/db 默认值", false, int64Ptr(0), nil),
		intKey("db.max_idle_conns", 0, "空闲连接数；0 沿用 pkg/db 默认值", false, int64Ptr(0), nil),
		durationKey("db.conn_max_lifetime", "0s", "连接最长存活时间；0 沿用默认值", false),
		stringKey("redis.addr", "", "Redis 地址；空表示不启用", false),
		intKey("redis.db", 0, "Redis 逻辑库编号", false, int64Ptr(0), nil),

		// 对象存储：云厂商凭证与桶级配置不进配置中心。
		stringKeyWithEnum("storage.driver", "memory", "对象存储驱动", false,
			"memory", "local", "s3", "oss", "cos", "obs"),
		stringKey("storage.key_prefix", "", "对象键前缀", false),
		intKey("storage.max_object_bytes", 0, "单对象字节上限；0 表示不限制", false, int64Ptr(0), nil),
		durationKey("storage.presign_put_ttl", "0s", "预签名上传有效期", false),
		durationKey("storage.presign_get_ttl", "0s", "预签名下载有效期", false),

		// 后台任务与追踪。
		durationKey("worker.poll_interval", "5s", "后台 worker 轮询间隔", true),
		stringKeyWithEnum("tracing.exporter", "none", "追踪导出器", false,
			"none", "otlp", "stdout", "noop"),
		stringKey("tracing.endpoint", "", "OTLP/HTTP 端点；exporter=otlp 时必填", false),
		boolKey("tracing.insecure", "false", "OTLP 明文传输", false),
		stringKeyWithEnum("id.node_id_source", "env", "雪花节点号来源", false,
			"config", "env", "statefulset"),

		// 幂等：TTL 支持热更，其余影响中间件装配。
		boolKey("idempotency.enabled", "false", "是否启用写请求幂等", false),
		stringKeyWithEnum("idempotency.driver", "memory", "幂等存储驱动", false, "memory", "redis"),
		stringKey("idempotency.header", "Idempotency-Key", "幂等键请求头", false),
		durationKey("idempotency.ttl", "24h", "幂等记录存活时间", true),
		boolKey("idempotency.required", "false", "缺少幂等键是否拒绝写请求", false),
		intKey("idempotency.max_body_bytes", 1<<20, "参与幂等指纹的请求体上限", false, int64Ptr(0), nil),
		boolKey("idempotency.degrade_on_error", "false", "幂等存储故障时是否放行", false),

		// 限流：阈值与空闲回收支持热更，驱动与分键影响装配。
		boolKey("rate_limit.enabled", "false", "是否启用入口限流", false),
		stringKeyWithEnum("rate_limit.driver", "memory", "限流存储驱动", false, "memory", "redis"),
		stringKeyWithEnum("rate_limit.key", "channel", "限流分键策略", false,
			"channel", "channel_user", "channel_tenant_user", "client"),
		stringKeyWithEnum("rate_limit.failure_mode", "fail_open", "Redis 故障策略", false,
			"fail_open", "fail_closed"),
		stringKeyWithPattern("rate_limit.rps", "0", "每秒补充令牌数", true, `^[0-9]+(\.[0-9]+)?$`),
		intKey("rate_limit.burst", 0, "令牌桶容量", true, int64Ptr(0), nil),
		durationKey("rate_limit.idle_ttl", "10m", "空闲限流桶回收阈值", true),
		boolKey("rate_limit.degrade_on_failure", "false", "连续故障后是否进入宽松降级", true),

		// 异步运行时：投递/消费参数支持热更，驱动与 broker 启停需重启。
		boolKey("async.enabled", "false", "是否启用异步运行时", false),
		stringKeyWithEnum("async.driver", "db", "异步驱动", false, "db", "nats"),
		boolKey("async.outbox.publish_in_api", "false", "API 进程内是否运行 Outbox Relay", false),
		durationKey("async.outbox.poll_interval", "1s", "Outbox 无事件休眠间隔", true),
		intKey("async.outbox.batch_size", 100, "Outbox 单轮认领上限", true, int64Ptr(1), int64Ptr(500)),
		durationKey("async.outbox.lease", "30s", "Outbox processing 租约", true),
		intKey("async.outbox.max_attempts", 10, "Outbox 最大投递次数", true, int64Ptr(1), int64Ptr(20)),
		intKey("async.outbox.concurrency", 2, "Outbox Relay 并发 worker 数", true, int64Ptr(1), int64Ptr(16)),
		intKey("async.consumer.concurrency", 4, "消费并发度", true, int64Ptr(1), int64Ptr(32)),
		durationKey("async.consumer.visibility_timeout", "60s", "消费幂等记录接管窗口", true),
		stringKeyWithEnum("async.consumer.retry_backoff", "exponential", "消费重试退避策略", true,
			"exponential", "fixed"),
		stringKey("async.broker.stream", "async", "JetStream 流名称", false),
		stringKey("async.broker.subject_prefix", "async", "JetStream 主题前缀", false),
		durationKey("async.broker.ack_wait", "30s", "单条消息确认超时", false),
		intKey("async.broker.max_deliver", 10, "最大投递次数；超过进入 DLQ", false,
			int64Ptr(1), int64Ptr(100)),
	}
}

// iamSpecs 返回 IAM 治理键声明；模式由部署显式声明，不播种默认值。
func iamSpecs() []domain.KeySpec {
	return []domain.KeySpec{
		{
			Namespace:   NamespaceIAM,
			Key:         "iam.mode",
			ValueType:   domain.ValueTypeString,
			Description: "IAM 运行模式；空值按 off 处理",
			Owner:       ownerIAM,
			Enum:        []string{"off", "shadow", "enforce"},
		},
	}
}

// stringKey 构造字符串键声明。
func stringKey(key, defaultValue, description string, reloadable bool) domain.KeySpec {
	return domain.KeySpec{
		Namespace: NamespacePlatform, Key: key, ValueType: domain.ValueTypeString,
		Default: defaultValue, Description: description, Owner: ownerPlatform, Reloadable: reloadable,
	}
}

// stringKeyWithEnum 构造带枚举约束的字符串键声明。
func stringKeyWithEnum(key, defaultValue, description string, reloadable bool, enum ...string) domain.KeySpec {
	spec := stringKey(key, defaultValue, description, reloadable)
	spec.Enum = enum
	return spec
}

// stringKeyWithPattern 构造带正则约束的字符串键声明。
func stringKeyWithPattern(key, defaultValue, description string, reloadable bool, pattern string) domain.KeySpec {
	spec := stringKey(key, defaultValue, description, reloadable)
	spec.Pattern = pattern
	return spec
}

// intKey 构造整数键声明。
func intKey(key string, defaultValue int64, description string, reloadable bool,
	minValue, maxValue *int64) domain.KeySpec {
	return domain.KeySpec{
		Namespace: NamespacePlatform, Key: key, ValueType: domain.ValueTypeInt,
		Default: strconv.FormatInt(defaultValue, 10), Description: description, Owner: ownerPlatform,
		Reloadable: reloadable, MinInt: minValue, MaxInt: maxValue,
	}
}

// durationKey 构造时长键声明。
func durationKey(key, defaultValue, description string, reloadable bool) domain.KeySpec {
	return domain.KeySpec{
		Namespace: NamespacePlatform, Key: key, ValueType: domain.ValueTypeDuration,
		Default: defaultValue, Description: description, Owner: ownerPlatform, Reloadable: reloadable,
	}
}

// boolKey 构造布尔键声明。
func boolKey(key, defaultValue, description string, reloadable bool) domain.KeySpec {
	return domain.KeySpec{
		Namespace: NamespacePlatform, Key: key, ValueType: domain.ValueTypeBool,
		Default: defaultValue, Description: description, Owner: ownerPlatform, Reloadable: reloadable,
	}
}

// requiredKey 构造无默认值的必填键声明。
func requiredKey(key string, valueType domain.ValueType, description string) domain.KeySpec {
	return domain.KeySpec{
		Namespace: NamespacePlatform, Key: key, ValueType: valueType,
		Description: description, Owner: ownerPlatform, Required: true,
	}
}

// int64Ptr 返回整数指针（声明区间用）。
func int64Ptr(value int64) *int64 {
	return &value
}
