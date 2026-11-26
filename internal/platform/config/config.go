package config

import (
	"time"

	platformstorage "github.com/admin-frame/admin-frame/internal/platform/storage"
)

// EnvPrefix 是配置环境变量前缀；组合加载层（cmd/internal/appconfig）复用它绑定域配置。
const EnvPrefix = "ADMIN_FRAME"

const envPrefix = EnvPrefix

// InfraEnvPathAllowlist 是业务进程唯一允许的 ADMIN_FRAME_* 环境覆盖根段。
//
// 仅基础设施连接参数（db / redis，含 username/password）；业务键与 JWT 等
// 密钥不得经环境变量注入，须走签名快照或 secretclient → config-api。
var InfraEnvPathAllowlist = []string{"db", "redis"}

// maskedValue 是日志中敏感字段的占位符（SECURITY_STANDARDS SEC-05）。
const maskedValue = "***"

// 鉴权模式（auth.mode）。
const (
	// AuthModeDemo 演示令牌；仅允许 local / dev / test。
	AuthModeDemo = "demo"
	// AuthModeJWT 使用 pkg/jwt 校验真实令牌。
	AuthModeJWT = "jwt"
)

// Config 进程配置根对象（按 cmd 对应 yaml / 签名快照加载）。
//
// 环境变量仅放行 db/redis 根段（InfraEnvPathAllowlist + BindEnvFiltered）；
// 业务字段不得经 ADMIN_FRAME_* 注入。
type Config struct {
	// Env 部署环境：local / dev / test / staging / prod（可空，表示未声明）。
	Env             string                 `yaml:"env"`
	Service         string                 `yaml:"service"`
	Channel         string                 `yaml:"channel"`
	Addr            string                 `yaml:"addr"`
	ShutdownTimeout time.Duration          `yaml:"shutdown_timeout"`
	HTTP            HTTPConfig             `yaml:"http"`
	Log             LogConfig              `yaml:"log"`
	Auth            AuthConfig             `yaml:"auth"`
	DB              DBConfig               `yaml:"db"`
	Redis           RedisConfig            `yaml:"redis"`
	Storage         platformstorage.Config `yaml:"storage"`
	Worker          WorkerConfig           `yaml:"worker"`
	Tracing         TracingConfig          `yaml:"tracing"`
	ID              IDConfig               `yaml:"id"`
	Idempotency     IdempotencyConfig      `yaml:"idempotency"`
	RateLimit       RateLimitConfig        `yaml:"rate_limit"`
	Async           AsyncConfig            `yaml:"async"`
	CORS            CORSConfig             `yaml:"cors"`
	Secrets         SecretsConfig          `yaml:"secrets"`
}
