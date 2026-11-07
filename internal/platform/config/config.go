package config

import (
	"time"

	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
)

const envPrefix = "ADMIN_FRAME"

// maskedValue 是日志中敏感字段的占位符（SECURITY_STANDARDS SEC-05）。
const maskedValue = "***"

// 鉴权模式（auth.mode）。
const (
	// AuthModeDemo 演示令牌；仅允许 local / dev / test。
	AuthModeDemo = "demo"
	// AuthModeJWT 使用 pkg/jwt 校验真实令牌。
	AuthModeJWT = "jwt"
)

// Config 进程配置根对象（按 cmd 对应 yaml 加载）。
//
// yaml 标签同时决定环境变量名：Env → ADMIN_FRAME_ENV、Log.Level →
// ADMIN_FRAME_LOG_LEVEL（pkg/config.BindEnv）。新增字段无需再改环境变量代码。
type Config struct {
	// Env 部署环境：local / dev / test / staging / prod（可空，表示未声明）。
	Env             string                        `yaml:"env"`
	Service         string                        `yaml:"service"`
	Channel         string                        `yaml:"channel"`
	Addr            string                        `yaml:"addr"`
	ShutdownTimeout time.Duration                 `yaml:"shutdown_timeout"`
	HTTP            HTTPConfig                    `yaml:"http"`
	Log             LogConfig                     `yaml:"log"`
	Auth            AuthConfig                    `yaml:"auth"`
	DB              DBConfig                      `yaml:"db"`
	Redis           RedisConfig                   `yaml:"redis"`
	Storage         platformstorage.StorageConfig `yaml:"storage"`
	Worker          WorkerConfig                  `yaml:"worker"`
	Tracing         TracingConfig                 `yaml:"tracing"`
	ID              IDConfig                      `yaml:"id"`
	Idempotency     IdempotencyConfig             `yaml:"idempotency"`
	RateLimit       RateLimitConfig               `yaml:"rate_limit"`
	Async           AsyncConfig                   `yaml:"async"`
	CORS            CORSConfig                    `yaml:"cors"`
	IAM             IAMConfig                     `yaml:"iam"`
	Identity        IdentityConfig                `yaml:"identity"`
	Audit           AuditConfig                   `yaml:"audit"`
	Tenant          TenantConfig                  `yaml:"tenant"`
	Secrets         SecretsConfig                 `yaml:"secrets"`

	// secretErr 暂存 normalize 阶段的密钥解析错误。
	//
	// Loader 的 Normalize 钩子无法返回错误，因此把结果记录在此，由 Validate
	// 汇总进启动错误。不导出、不参与 yaml / 环境变量绑定。
	secretErr error
}
