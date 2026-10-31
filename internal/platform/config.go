package platform

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/security"
	"github.com/admin-frame/admin-frame/pkg/config"
	"github.com/admin-frame/admin-frame/pkg/password"
)

// envPrefix 是本进程所有环境变量的统一前缀（CONFIG_STANDARDS CFG-04）。
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
	Env             string            `yaml:"env"`
	Service         string            `yaml:"service"`
	Channel         string            `yaml:"channel"`
	Addr            string            `yaml:"addr"`
	ShutdownTimeout time.Duration     `yaml:"shutdown_timeout"`
	HTTP            HTTPConfig        `yaml:"http"`
	Log             LogConfig         `yaml:"log"`
	Auth            AuthConfig        `yaml:"auth"`
	DB              DBConfig          `yaml:"db"`
	Redis           RedisConfig       `yaml:"redis"`
	Storage         StorageConfig     `yaml:"storage"`
	Worker          WorkerConfig      `yaml:"worker"`
	Tracing         TracingConfig     `yaml:"tracing"`
	ID              IDConfig          `yaml:"id"`
	Idempotency     IdempotencyConfig `yaml:"idempotency"`
	RateLimit       RateLimitConfig   `yaml:"rate_limit"`
	Async           AsyncConfig       `yaml:"async"`
	CORS            CORSConfig        `yaml:"cors"`
	IAM             IAMConfig         `yaml:"iam"`
	Identity        IdentityConfig    `yaml:"identity"`
}

// 身份域撤销存储驱动（identity.revocation.driver）。
const (
	// IdentityRevocationDriverRedis 跨副本共享撤销状态；生产唯一实现。
	IdentityRevocationDriverRedis = "redis"
)

// 短信驱动（identity.sms.driver）。
const (
	// IdentitySMSDriverMemory 进程内驱动，仅用于 local / dev / test。
	IdentitySMSDriverMemory = "memory"
	// IdentitySMSDriverAliyun 阿里云短信驱动。
	IdentitySMSDriverAliyun = "aliyun"
)

// IdentityConfig 是身份域认证参数（docs/identity-authn.md §10）。
//
// 默认 enabled=false：未显式开启时保持「只有验签」的既有行为，升级不改变鉴权来源。
type IdentityConfig struct {
	// Enabled 是否启用登录 / 刷新 / 登出端点与撤销检查。
	Enabled bool `yaml:"enabled"`
	// AccessTTL 访问令牌有效期；[5m, 1h]。
	AccessTTL time.Duration `yaml:"access_ttl"`
	// RefreshTTL 刷新令牌有效期；必须大于 AccessTTL。
	RefreshTTL time.Duration `yaml:"refresh_ttl"`
	// Session 会话策略。
	Session IdentitySessionConfig `yaml:"session"`
	// Retention 身份域数据保留与清理策略。
	Retention IdentityRetentionConfig `yaml:"retention"`
	// Revocation 撤销存储参数。
	Revocation IdentityRevocationConfig `yaml:"revocation"`
	// Lockout 登录失败锁定策略。
	Lockout IdentityLockoutConfig `yaml:"lockout"`
	// LoginRateLimit 登录限流参数。
	LoginRateLimit IdentityLoginRateLimitConfig `yaml:"login_rate_limit"`
	// RefreshRateLimit refresh / logout 端点限流参数（SEC-04）。
	RefreshRateLimit IdentityLoginRateLimitConfig `yaml:"refresh_rate_limit"`
	// Password 密码强度策略（IDN-P2-01）；未配置时使用内置默认值。
	Password IdentityPasswordConfig `yaml:"password"`
	// HashKey 是 PII / 短信码摘要的 HMAC 主密钥（32 字节），生产必填（IDN-P1-06）。
	HashKey string `yaml:"hash_key"`
	// HashKeyID 写入摘要前缀；默认 v1。
	HashKeyID string `yaml:"hash_key_id"`
	// PreviousHashKeys 轮换期的历史密钥，用于双读。
	PreviousHashKeys []IdentityHashKeyConfig `yaml:"previous_hash_keys"`
	// MFA A 端多因素认证参数。
	MFA IdentityMFAConfig `yaml:"mfa"`
	// SMS C 端短信验证码参数。
	SMS IdentitySMSConfig `yaml:"sms"`
	// OAuth C 端第三方授权参数。
	OAuth IdentityOAuthConfig `yaml:"oauth"`
	// Subject 账号登录主体的 PII 加密 / 索引参数（IDN-GAP-P1-06）。
	//
	// 未配置时不启用双写与加密索引，登录仍按明文 subject 查询；
	// 配置后进入 S1/S2 阶段：明文保留，同时写 subject_index / subject_ciphertext。
	Subject IdentitySubjectConfig `yaml:"subject"`
}

// IdentitySubjectConfig 是登录主体（PII）的加密与确定性索引参数。
//
// subject_index 由 identity.hash_key 计算 HMAC，复用既有摘要轮换；
// subject_ciphertext 由本配置的 AES-256-GCM 密钥环加密，AAD 绑定 account_id。
type IdentitySubjectConfig struct {
	// Keys 加密密钥环，支持按 key id 轮换。
	Keys []IdentityMFAKeyConfig `yaml:"keys"`
	// ActiveKeyID 当前加密密钥；Keys 非空时必填。
	ActiveKeyID string `yaml:"active_key_id"`
	// LegacyKey 旧格式单密钥，仅用于解密历史密文；可为空。
	LegacyKey string `yaml:"legacy_key"`
}

// DecodeKeys 解码主体加密密钥环；未配置时返回 nil。
func (s IdentitySubjectConfig) DecodeKeys() (map[string][]byte, error) {
	if len(s.Keys) == 0 {
		return nil, nil
	}
	keys := make(map[string][]byte, len(s.Keys))
	for _, item := range s.Keys {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, errors.New("identity.subject.keys id is required")
		}
		if len(id) > 16 {
			return nil, errors.New("identity.subject.keys id must be <= 16 bytes")
		}
		if _, dup := keys[id]; dup {
			return nil, fmt.Errorf("identity.subject.keys has duplicate id: %s", id)
		}
		key, err := decodeKey(item.Key, "identity.subject.keys.key")
		if err != nil {
			return nil, err
		}
		if key == nil {
			return nil, fmt.Errorf("identity.subject.keys key is required for id %s", id)
		}
		keys[id] = key
	}
	return keys, nil
}

// DecodeLegacyKey 解码旧格式单密钥；未配置返回 nil。
func (s IdentitySubjectConfig) DecodeLegacyKey() ([]byte, error) {
	if strings.TrimSpace(s.LegacyKey) == "" {
		return nil, nil
	}
	return decodeKey(s.LegacyKey, "identity.subject.legacy_key")
}

// IdentitySMSConfig 是 C 端短信验证码参数（docs/identity-authn.md §8.1 / §10）。
type IdentitySMSConfig struct {
	// Enabled 是否启用短信验证码发送端点；仅 C 端允许开启。
	Enabled bool `yaml:"enabled"`
	// Driver 短信驱动：memory（本地/测试）| aliyun；生产禁止 memory。
	Driver string `yaml:"driver"`
	// TemplateID 厂商模板编码；启用时必填。
	TemplateID string `yaml:"template_id"`
	// SignName 短信签名；国际短信可为空。
	SignName string `yaml:"sign_name"`
	// Locale 语言/地区提示（如 zh-CN）。
	Locale string `yaml:"locale"`
	// CodeTTL 验证码有效期，默认 5m，上限 15m。
	CodeTTL time.Duration `yaml:"code_ttl"`
	// MaxAttempts 单个验证码允许的最大错误次数，默认 5。
	MaxAttempts int `yaml:"max_attempts"`
	// ResendInterval 同一手机号最小重发间隔，默认 60s。
	ResendInterval time.Duration `yaml:"resend_interval"`
	// DailyPerPhone 同一手机号每日发送上限，默认 10。
	DailyPerPhone int `yaml:"daily_per_phone"`
	// HourlyPerIP 同一 IP 每小时发送上限，默认 30。
	HourlyPerIP int `yaml:"hourly_per_ip"`
	// DailyBudget 全局日发送预算；0 表示不限制（IDN-P1-04）。
	DailyBudget int `yaml:"daily_budget"`
	// BreakerFailures 连续失败达到该值后打开供应商熔断；0 表示默认 5（IDN-P2-05）。
	BreakerFailures int `yaml:"breaker_failures"`
	// BreakerTimeout 熔断打开持续时间；0 表示默认 60s，范围 [1s, 10m]。
	BreakerTimeout time.Duration `yaml:"breaker_timeout"`
}

// SMS 供应商熔断默认值（IDN-P2-05）。
const (
	// identityDefaultSMSBreakerFailures 连续失败阈值。
	identityDefaultSMSBreakerFailures = 5
	// identityDefaultSMSBreakerTimeout 熔断打开时长。
	identityDefaultSMSBreakerTimeout = 60 * time.Second
	// identityMinSMSBreakerTimeout 允许配置的最小熔断时长。
	identityMinSMSBreakerTimeout = time.Second
	// identityMaxSMSBreakerTimeout 允许配置的最大熔断时长。
	identityMaxSMSBreakerTimeout = 10 * time.Minute
)

// IdentityOAuthConfig 是 C 端 OAuth 授权码登录参数（docs/identity-authn.md §8.2 / §10）。
type IdentityOAuthConfig struct {
	// AutoProvision 未映射身份是否自动建号；默认关闭（失败关闭）。
	AutoProvision bool `yaml:"auto_provision"`
	// RateLimit authorize / callback 的 IP 级与全局限流（IDN-P2-05）。
	RateLimit IdentityLoginRateLimitConfig `yaml:"rate_limit"`
	// Timeout 单次 IdP 调用超时；0 表示默认 5s，范围 [1s, 30s]（PERF-06）。
	Timeout time.Duration `yaml:"timeout"`
	// BreakerFailures 连续失败阈值；0 表示默认 5。
	BreakerFailures int `yaml:"breaker_failures"`
	// BreakerTimeout 熔断打开持续时间；0 表示默认 60s，范围 [1s, 10m]。
	BreakerTimeout time.Duration `yaml:"breaker_timeout"`
	// Providers 已配置的第三方身份提供方；为空时不注册 OAuth 路由。
	Providers []IdentityOAuthProviderConfig `yaml:"providers"`
}

// IdentityOAuthProviderConfig 是单个 OAuth2 提供方参数。
//
// client_secret 只来自环境变量（由 ClientSecretEnv 指定变量名），禁止写入 YAML。
type IdentityOAuthProviderConfig struct {
	// Name 提供方名，写入路由与映射表；小写字母开头，允许 [a-z0-9_-]。
	Name string `yaml:"name"`
	// ClientID OAuth2 客户端 ID。
	ClientID string `yaml:"client_id"`
	// ClientSecretEnv 保存 client_secret 的环境变量名。
	ClientSecretEnv string `yaml:"client_secret_env"`
	// AuthURL 授权端点。
	AuthURL string `yaml:"auth_url"`
	// TokenURL 令牌端点。
	TokenURL string `yaml:"token_url"`
	// UserInfoURL 用户信息端点，用于读取外部主体标识。
	UserInfoURL string `yaml:"userinfo_url"`
	// RedirectURI 回调地址，须与 IdP 登记一致。
	RedirectURI string `yaml:"redirect_uri"`
	// Scopes 授权范围。
	Scopes []string `yaml:"scopes"`
	// SubjectField userinfo JSON 中主体标识字段名，默认 sub。
	SubjectField string `yaml:"subject_field"`
}

// identityDefaultMFAIssuer 与 internal/identity/app.DefaultMFAIssuer 保持一致。
const identityDefaultMFAIssuer = "AdminFrame"

// IdentityMFAConfig 是 A 端多因素认证参数（docs/identity-authn.md §10）。
type IdentityMFAConfig struct {
	// AdminRequired 表示 A 端密码登录后必须完成 TOTP 才能获得管理令牌；生产强制开启。
	AdminRequired bool `yaml:"admin_required"`
	// Issuer 写入 otpauth URI，默认 AdminFrame。
	Issuer string `yaml:"issuer"`
	// SecretKey 是 TOTP 密钥的 AES-256 主密钥，仅允许通过环境变量注入（SEC-01）。
	// 未配置 keys 时作为唯一密钥；配置 keys 后作为旧密文的解密回退。
	SecretKey string `yaml:"secret_key"`
	// Keys 是 MFA 密钥环，支持按 key id 轮换（IDN-P1-05）。
	Keys []IdentityMFAKeyConfig `yaml:"keys"`
	// ActiveKeyID 指定当前加密密钥；Keys 非空时必填。
	ActiveKeyID string `yaml:"active_key_id"`
}

// IdentityMFAKeyConfig 是密钥环中的一项。
type IdentityMFAKeyConfig struct {
	// ID key id，最长 16 字节，写入密文头。
	ID string `yaml:"id"`
	// Key 32 字节密钥（raw / hex / base64 / base64url），生产走环境变量。
	Key string `yaml:"key"`
}

// DecodeKeys 解码密钥环；id 重复、缺失或密钥非法时返回错误。
func (m IdentityMFAConfig) DecodeKeys() (map[string][]byte, error) {
	if len(m.Keys) == 0 {
		return nil, nil
	}
	keys := make(map[string][]byte, len(m.Keys))
	for _, item := range m.Keys {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return nil, errors.New("identity.mfa.keys id is required")
		}
		if len(id) > 16 {
			return nil, errors.New("identity.mfa.keys id must be <= 16 bytes")
		}
		if _, dup := keys[id]; dup {
			return nil, fmt.Errorf("identity.mfa.keys has duplicate id: %s", id)
		}
		key, err := decodeMFAKey(item.Key)
		if err != nil {
			return nil, err
		}
		if key == nil {
			return nil, fmt.Errorf("identity.mfa.keys key is required for id %s", id)
		}
		keys[id] = key
	}
	return keys, nil
}

// DecodeSecretKey 解码 mfa.secret_key；未配置时返回 nil，不做长度校验。
//
// 依次尝试 32 字节原始值、hex、标准 Base64、RawURL Base64，均失败返回错误。
func (m IdentityMFAConfig) DecodeSecretKey() ([]byte, error) {
	if strings.TrimSpace(m.SecretKey) == "" {
		return nil, nil
	}
	return decodeMFAKey(m.SecretKey)
}

// decodeMFAKey 解码 32 字节密钥；空值返回 (nil, nil)。
func decodeMFAKey(value string) ([]byte, error) {
	return decodeKey(value, "identity.mfa.secret_key")
}

// decodeKey 解码 32 字节密钥并附带字段路径；空值返回 (nil, nil)。
func decodeKey(value, field string) ([]byte, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil, nil
	}
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(raw); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("%s must be a 32-byte key encoded as raw, hex, base64 or base64url", field)
}

// IdentitySessionConfig 是会话策略参数。
type IdentitySessionConfig struct {
	// IdleTimeout 会话空闲上限；必须为正且不超过 refresh_ttl。
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// AbsoluteTTL 设备绝对生命上限；刷新令牌只能在该上限内滑动续期（IDN-P1-07）。
	// 必须大于 refresh_ttl，默认 720h（30d）。
	AbsoluteTTL time.Duration `yaml:"absolute_ttl"`
	// MaxPerAccount 单账号最大活跃会话数。
	MaxPerAccount int `yaml:"max_per_account"`
	// RefreshGrace 并发刷新宽限窗口；0 表示禁用，默认 10s，上限 60s（REL-06）。
	//
	// 窗口内同一已轮换令牌的并发重试只返回 40100，不撤销整链；
	// 超出窗口仍按真实重放撤销整条会话链。
	RefreshGrace time.Duration `yaml:"refresh_grace"`
}

// IdentityRetentionConfig 是身份域数据保留与清理策略（IDN-P1-01）。
type IdentityRetentionConfig struct {
	// SessionDays rotated / revoked / expired 会话与设备的保留天数；生产 ≥7。
	SessionDays int `yaml:"session_days"`
	// LoginAttemptDays 登录尝试记录保留天数；生产 ≥180。
	LoginAttemptDays int `yaml:"login_attempt_days"`
	// ArchiveDays 归档表保留天数；0 使用默认 365，负数关闭归档（回退到只删不归档）。
	ArchiveDays int `yaml:"archive_days"`
	// CleanupInterval worker 清理任务执行间隔；默认 1h。
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
}

// 账号撤销快照陈旧度边界，与 internal/identity/app 保持一致（docs/identity-authn.md §6.1）。
const (
	// identityMinRevocationStateTTL 快照最小陈旧度，避免每请求回源。
	identityMinRevocationStateTTL = time.Second
	// identityMaxRevocationStateTTL 快照硬上限，对应「≤60s 生效」的安全承诺。
	identityMaxRevocationStateTTL = 60 * time.Second
)

// IdentityRevocationConfig 是撤销存储参数。
type IdentityRevocationConfig struct {
	// Driver 驱动；当前仅 redis。
	Driver string `yaml:"driver"`
	// FailureMode Redis 故障策略：fail_closed（默认）| fail_open。
	FailureMode string `yaml:"failure_mode"`
	// StateTTL 账号撤销快照最大陈旧度；超过即强制回源数据库，上限 60s。
	StateTTL time.Duration `yaml:"state_ttl"`
	// LocalCacheTTL 进程内 L1 快照 TTL；0 使用默认 1s，负数关闭缓存（IDN-GAP-P1-02）。
	//
	// 该值同时是「撤销在本副本可见」的最长延迟；上限 5s，跨副本仍由 state_ttl 收敛。
	LocalCacheTTL time.Duration `yaml:"local_cache_ttl"`
	// LocalCacheCapacity L1 单个类别（账号 / 会话）的条目上限；0 使用默认 4096。
	LocalCacheCapacity int `yaml:"local_cache_capacity"`
}

// 撤销本地缓存参数（IDN-GAP-P1-02）。
const (
	// identityDefaultRevocationLocalCacheTTL 默认 L1 TTL，与 ≤1s 撤销可见承诺一致。
	identityDefaultRevocationLocalCacheTTL = time.Second
	// identityMaxRevocationLocalCacheTTL L1 TTL 硬上限，避免配置过大放大撤销延迟。
	identityMaxRevocationLocalCacheTTL = 5 * time.Second
	// identityDefaultRevocationLocalCacheCapacity 默认 L1 容量上限。
	identityDefaultRevocationLocalCacheCapacity = 4096
	// identityMaxRevocationLocalCacheCapacity 允许配置的 L1 容量上限。
	identityMaxRevocationLocalCacheCapacity = 100000
)

// 密码策略边界，与 internal/identity/app 默认值保持一致（docs/identity-authn.md §10）。
const (
	// identityDefaultPasswordMinLength 默认最小字符数。
	identityDefaultPasswordMinLength = 12
	// identityDefaultPasswordMaxLength 默认最大字符数。
	identityDefaultPasswordMaxLength = 128
	// identityMinPasswordLength 允许配置的最小长度下限。
	identityMinPasswordLength = 8
)

// 密码校验并发保护默认值（IDN-P2-05）。
const (
	// identityDefaultPasswordWaitTimeout 获取并发槽位的默认等待上限。
	identityDefaultPasswordWaitTimeout = 2 * time.Second
	// identityMinPasswordWaitTimeout 允许配置的最小等待上限。
	identityMinPasswordWaitTimeout = 100 * time.Millisecond
	// identityMaxPasswordWaitTimeout 允许配置的最大等待上限。
	identityMaxPasswordWaitTimeout = 30 * time.Second
)

// IdentityPasswordConfig 是密码强度策略（IDN-P2-01）与 Argon2 并发保护（IDN-P2-05）。
//
// 零值字段由默认策略填充：12～128 字符且要求复杂度；显式配置时以配置为准。
type IdentityPasswordConfig struct {
	// MinLength 最小字符数；0 表示使用默认 12。
	MinLength int `yaml:"min_length"`
	// MaxLength 最大字符数；0 表示使用默认 128。
	MaxLength int `yaml:"max_length"`
	// RequireComplexity 是否要求至少三类字符；nil 表示使用默认 true。
	RequireComplexity *bool `yaml:"require_complexity"`
	// MaxConcurrent 同时执行的密码哈希 / 校验上限；0 表示默认 2×CPU（IDN-P2-05）。
	MaxConcurrent int `yaml:"max_concurrent"`
	// WaitTimeout 获取并发槽位的等待上限；0 表示默认 2s，超时返回 50300。
	WaitTimeout time.Duration `yaml:"wait_timeout"`
	// HistoryCount 密码历史保留条数；0 表示默认 5，用于阻止近期密码复用（SEC-06）。
	HistoryCount int `yaml:"history_count"`
	// HistoryWindow 密码复用限制窗口；0 表示默认 365 天。
	HistoryWindow time.Duration `yaml:"history_window"`
}

// IdentityLockoutConfig 是登录失败锁定参数。
type IdentityLockoutConfig struct {
	MaxFailures int           `yaml:"max_failures"`
	Window      time.Duration `yaml:"window"`
	Duration    time.Duration `yaml:"duration"`
}

// IdentityHashKeyConfig 是摘要历史密钥。
type IdentityHashKeyConfig struct {
	// ID key id，仅允许字母 / 数字 / 下划线 / 短横线，最长 16 字节。
	ID string `yaml:"id"`
	// Key 32 字节密钥（raw / hex / base64 / base64url）。
	Key string `yaml:"key"`
}

// DecodeHashKeys 解码摘要密钥环；未配置主密钥时返回空。
func (c IdentityConfig) DecodeHashKeys() (active string, keys map[string][]byte, err error) {
	active = strings.TrimSpace(c.HashKeyID)
	if active == "" && strings.TrimSpace(c.HashKey) != "" {
		active = "v1"
	}
	keys = make(map[string][]byte, 1+len(c.PreviousHashKeys))
	if strings.TrimSpace(c.HashKey) == "" {
		if len(c.PreviousHashKeys) > 0 {
			return "", nil, errors.New("identity.previous_hash_keys requires identity.hash_key")
		}
		return "", nil, nil
	}
	key, err := decodeKey(c.HashKey, "identity.hash_key")
	if err != nil {
		return "", nil, err
	}
	if key == nil {
		return "", nil, errors.New("identity.hash_key is required")
	}
	keys[active] = key
	for _, item := range c.PreviousHashKeys {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return "", nil, errors.New("identity.previous_hash_keys id is required")
		}
		if _, dup := keys[id]; dup {
			return "", nil, fmt.Errorf("identity.previous_hash_keys has duplicate id: %s", id)
		}
		previous, decodeErr := decodeKey(item.Key, "identity.previous_hash_keys.key")
		if decodeErr != nil {
			return "", nil, decodeErr
		}
		if previous == nil {
			return "", nil, fmt.Errorf("identity.previous_hash_keys key is required for id %s", id)
		}
		keys[id] = previous
	}
	return active, keys, nil
}

// IdentityLoginRateLimitConfig 是登录限流参数（账号级与 IP 级双维度）。
type IdentityLoginRateLimitConfig struct {
	RPS   float64 `yaml:"rps"`
	Burst int     `yaml:"burst"`
	// IPRPS / IPBurst 是 IP 级独立配额；0 表示默认 20 rps / burst 40（PERF-05）。
	IPRPS  float64 `yaml:"ip_rps"`
	IPBurst int    `yaml:"ip_burst"`
	// FailureMode Redis 故障策略：fail_closed（默认）| fail_open；prod/staging 强制 fail_closed。
	FailureMode string `yaml:"failure_mode"`
}

// ID 节点号来源（id.node_id_source）。
const (
	// IDSourceConfig 使用显式 id.node_id（环境变量覆盖等价）。
	IDSourceConfig = "config"
	// IDSourceEnv 保留来源语义，实际同样读取 id.node_id（pkg/config 已合并环境变量）。
	IDSourceEnv = "env"
	// IDSourceStatefulSet 从 HOSTNAME 末尾 ordinal 派生 NodeID。
	IDSourceStatefulSet = "statefulset"
)

// IDConfig 是分布式 ID 生成参数（多副本必须使用不同节点号）。
type IDConfig struct {
	// NodeID 节点号 [0, 1023]；未配置表示未显式声明。
	NodeID *int64 `yaml:"node_id"`
	// NodeIDSource config | env | statefulset。
	NodeIDSource string `yaml:"node_id_source"`
}

// 幂等存储驱动。
const (
	// IdempotencyDriverMemory 进程内存储（单副本 / 本地）。
	IdempotencyDriverMemory = "memory"
	// IdempotencyDriverRedis 跨副本共享（prod/staging 启用时强制）。
	IdempotencyDriverRedis = "redis"
)

// IdempotencyConfig 是写请求幂等去重参数（internal/platform 统一挂载）。
type IdempotencyConfig struct {
	// Enabled 是否启用；默认关闭。
	Enabled bool `yaml:"enabled"`
	// Driver memory | redis；prod/staging 启用时必须 redis。
	Driver string `yaml:"driver"`
	// Header 幂等键请求头，默认 Idempotency-Key。
	Header string `yaml:"header"`
	// TTL 记录存活时间，默认 24h。
	TTL time.Duration `yaml:"ttl"`
	// Required 为 true 时写方法缺少幂等键直接拒绝。
	Required bool `yaml:"required"`
	// MaxBodyBytes 参与指纹与缓存的请求体上限，默认 1 MiB。
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
}

// 限流驱动与失败策略。
const (
	// RateLimitDriverMemory 进程内令牌桶（单副本）。
	RateLimitDriverMemory = "memory"
	// RateLimitDriverRedis 跨副本共享令牌桶（prod/staging 启用时强制）。
	RateLimitDriverRedis = "redis"
	// RateLimitFailureOpen Redis 故障时放行。
	RateLimitFailureOpen = "fail_open"
	// RateLimitFailureClosed Redis 故障时拒绝。
	RateLimitFailureClosed = "fail_closed"
)

// 限流分键策略。
const (
	// RateLimitKeyChannel 按端别分桶（默认，键数量有界）。
	RateLimitKeyChannel = "channel"
	// RateLimitKeyChannelUser 按端别 + 用户分桶，匿名时回退 IP。
	RateLimitKeyChannelUser = "channel_user"
	// RateLimitKeyChannelTenantUser 按端别 + 商户 + 用户分桶，匿名时回退 IP。
	RateLimitKeyChannelTenantUser = "channel_tenant_user"
	// RateLimitKeyClient 按客户端 IP 分桶。
	RateLimitKeyClient = "client"
)

// maxNodeID 与 pkg/id 的雪花节点号上限一致。
const maxNodeID = 1023

// 异步驱动与退避策略（async.driver / async.consumer.retry_backoff）。
const (
	// AsyncDriverDB 使用数据库事务性 Outbox（默认）。
	AsyncDriverDB = "db"
	// AsyncDriverNATS 在 DB Outbox 之上把事件发布到 NATS JetStream。
	AsyncDriverNATS = "nats"
	// AsyncBackoffExponential 指数退避。
	AsyncBackoffExponential = "exponential"
	// AsyncBackoffFixed 固定间隔重试。
	AsyncBackoffFixed = "fixed"
)

// AsyncConfig 是平台级异步基础设施参数（ENT-03）。
//
// 默认关闭；启用后由 Relay 消费 async_outbox，消费方须幂等。
// driver=nats 时 Relay 仍以 DB Outbox 为事实源，额外把事件发布到 JetStream。
type AsyncConfig struct {
	// Enabled 是否启用异步运行时；默认 false。
	Enabled bool `yaml:"enabled"`
	// Driver 驱动；db（默认）| nats。
	Driver string `yaml:"driver"`
	// Broker 外部消息驱动参数；driver=nats 时生效。
	Broker AsyncBrokerConfig `yaml:"broker"`
	// Outbox 投递参数。
	Outbox AsyncOutboxConfig `yaml:"outbox"`
	// Consumer 消费运行时参数。
	Consumer AsyncConsumerConfig `yaml:"consumer"`
}

// AsyncBrokerConfig 是 NATS JetStream 连接与投递参数。
//
// URL 可包含用户名/口令，生产建议通过 ADMIN_FRAME_ASYNC_BROKER_URL 注入，
// 避免把凭证写入 yaml（CFG-06）。
type AsyncBrokerConfig struct {
	// URL NATS 服务地址；driver=nats 时必填，支持 nats:// / tls:// / ws:// / wss://。
	URL string `yaml:"url"`
	// Stream JetStream 流名称；空值取 async。
	Stream string `yaml:"stream"`
	// SubjectPrefix 主题前缀；空值取 async，最终主题为 <prefix>.<topic>。
	SubjectPrefix string `yaml:"subject_prefix"`
	// AckWait 单条消息确认超时；[1s, 30m]，空值取 30s。
	AckWait time.Duration `yaml:"ack_wait"`
	// MaxDeliver 最大投递次数；超过后进入 DLQ；[1, 100]，空值取 10。
	MaxDeliver int `yaml:"max_deliver"`
}

// AsyncOutboxConfig 是 Outbox Relay 参数。
type AsyncOutboxConfig struct {
	// PublishInAPI 是否在 API 进程内启动 Relay；生产强制 false，由 worker 独占。
	PublishInAPI bool `yaml:"publish_in_api"`
	// PollInterval 无到期事件时的休眠；[100ms, 1m]。
	PollInterval time.Duration `yaml:"poll_interval"`
	// BatchSize 单 worker 每轮认领上限；[1, 500]。
	BatchSize int `yaml:"batch_size"`
	// Lease processing 租约；[5s, 10m]。
	Lease time.Duration `yaml:"lease"`
	// MaxAttempts 最大投递次数；重试耗尽进入死信；[1, 20]。
	MaxAttempts int `yaml:"max_attempts"`
	// Concurrency Relay 并发 worker 数；[1, 16]。
	Concurrency int `yaml:"concurrency"`
}

// AsyncConsumerConfig 是消费运行时参数。
type AsyncConsumerConfig struct {
	// Concurrency 消费并发度；[1, 32]。
	Concurrency int `yaml:"concurrency"`
	// VisibilityTimeout 消费幂等记录的处理中接管窗口；[5s, 30m]。
	VisibilityTimeout time.Duration `yaml:"visibility_timeout"`
	// RetryBackoff exponential | fixed。
	RetryBackoff string `yaml:"retry_backoff"`
}

// RedisConfig 是共享缓存 / 锁的 Redis 连接参数（可选）。
//
// Addr 为空表示本进程不启用 Redis；IAM `cache.driver=redis` 时必须配置。
// 口令只允许通过环境变量注入（SEC-01 / CFG-06）。
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

// 鉴权数据来源模式（iam.mode，docs/rbac-iam.md §15.1）。
const (
	// IAMModeOff 使用装配期静态角色表（回滚位）。
	IAMModeOff = "off"
	// IAMModeShadow 计算快照但按静态表放行，仅记录差异。
	IAMModeShadow = "shadow"
	// IAMModeEnforce 以 IAM 快照为唯一真相。
	IAMModeEnforce = "enforce"
)

// 快照缓存驱动。
const (
	// IAMCacheDriverMemory 仅进程内 L1（local / dev）。
	IAMCacheDriverMemory = "memory"
	// IAMCacheDriverRedis L1 + Redis（staging / prod enforce 必须）。
	IAMCacheDriverRedis = "redis"
)

// RBAC2 约束模式。
const (
	// IAMConstraintsModeOff 关闭约束校验。
	IAMConstraintsModeOff = "off"
	// IAMConstraintsModeShadow 只记录违规，不拦截（默认）。
	IAMConstraintsModeShadow = "shadow"
	// IAMConstraintsModeEnforce 违规写入返回 40900。
	IAMConstraintsModeEnforce = "enforce"
)

// IAMConfig 是 RBAC 权限管理（IAM）运行参数（docs/rbac-iam.md §12.1）。
//
// 默认 mode=off：未显式启用时保持历史静态权限表行为，避免升级即切换鉴权来源。
type IAMConfig struct {
	// Mode off | shadow | enforce。
	Mode string `yaml:"mode"`
	// LoadTimeout 回源 DB 超时上限；必须 ≤ 1s。
	LoadTimeout time.Duration `yaml:"load_timeout"`
	// Cache 两级快照缓存。
	Cache IAMCacheConfig `yaml:"cache"`
	// Limits 快照与角色规模上限，防止角色膨胀拖垮鉴权。
	Limits IAMLimitsConfig `yaml:"limits"`
	// Security 安全开关。
	Security IAMSecurityConfig `yaml:"security"`
	// Breakglass 破玻璃（限时超级权限）开关与允许主体。
	Breakglass IAMBreakglassConfig `yaml:"breakglass"`
	// Approval 高风险直写强制转审批开关。
	Approval IAMApprovalConfig `yaml:"approval"`
	// Constraints RBAC2 约束（互斥 / 基数 / 前置）的落库行为。
	Constraints IAMConstraintsConfig `yaml:"constraints"`
}

// IAMConstraintsConfig 是 RBAC2 约束的落库行为配置。
//
// 默认 shadow：只计算并记录违规，不拦截写入，便于在存量数据上先观测影子冲突；
// 确认无问题后切 enforce，违规写入返回 40900。
type IAMConstraintsConfig struct {
	// Mode off | shadow | enforce。
	Mode string `yaml:"mode"`
}

// IAMCacheConfig 是快照缓存参数。
type IAMCacheConfig struct {
	// Driver memory | redis；prod + enforce 必须 redis。
	Driver string `yaml:"driver"`
	// L1TTL 进程内 TTL（撤销最坏生效窗口）；≤ 5s。
	L1TTL time.Duration `yaml:"l1_ttl"`
	// L2TTL Redis TTL；≤ 15m 且必须大于 L1TTL。
	L2TTL time.Duration `yaml:"l2_ttl"`
	// NegativeTTL 空快照 TTL，防异常主体打穿 DB。
	NegativeTTL time.Duration `yaml:"negative_ttl"`
	// KeyPrefix 缓存域段，默认 iam。
	KeyPrefix string `yaml:"key_prefix"`
	// L1MaxEntries 进程内最大条目数；≤0 时使用 10000。
	L1MaxEntries int `yaml:"l1_max_entries"`
}

// IAMLimitsConfig 是快照与角色规模上限。
type IAMLimitsConfig struct {
	// MaxRolesPerSubject 单主体最大生效角色数。
	MaxRolesPerSubject int `yaml:"max_roles_per_subject"`
	// MaxPermissionsPerRole 单角色最大权限点数。
	MaxPermissionsPerRole int `yaml:"max_permissions_per_role"`
	// MaxBindingsPerSubject 单主体最大绑定数。
	MaxBindingsPerSubject int `yaml:"max_bindings_per_subject"`
	// MaxCustomRolesPerMerchant B 端自定义角色数上限。
	MaxCustomRolesPerMerchant int `yaml:"max_custom_roles_per_merchant"`
}

// IAMBreakglassConfig 是破玻璃开关；生产默认关闭。
type IAMBreakglassConfig struct {
	// Enabled 是否启用破玻璃。
	Enabled bool `yaml:"enabled"`
	// AllowedSubjects 允许申请破玻璃的主体白名单；prod 启用时必填。
	AllowedSubjects []string `yaml:"allowed_subjects"`
}

// IAMApprovalConfig 是「高风险直写强制转审批」开关；生产默认关闭。
//
// 启用后角色 / 绑定等直接写端点在 transport 层统一返回 40900，调用方必须改走
// change-requests；审批单应用由用例内部调用，不经过 HTTP，因此不受此开关影响。
type IAMApprovalConfig struct {
	// Enabled 是否要求高风险变更必须经审批单。
	Enabled bool `yaml:"enabled"`
}

// IAMSecurityConfig 是 IAM 安全开关。
type IAMSecurityConfig struct {
	// AllowWildcard 是否允许快照携带通配符；prod 强制 false。
	AllowWildcard bool `yaml:"allow_wildcard"`
	// RequireReason 高风险变更是否强制填写原因。
	RequireReason bool `yaml:"require_reason"`
}

// Enabled 报告 IAM 是否接管鉴权（shadow 与 enforce 都需要加载快照）。
func (c IAMConfig) Enabled() bool {
	return c.Mode == IAMModeShadow || c.Mode == IAMModeEnforce
}

// HTTPConfig 入口 HTTP Server 与请求链路的加固参数（REL-02 / REL-01）。
type HTTPConfig struct {
	// ReadHeaderTimeout 读取请求头的上限；防御 slowloris，固定建议 5s。
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	// ReadTimeout 读取整个请求（含 body）的上限；0 表示不限制（不推荐）。
	ReadTimeout time.Duration `yaml:"read_timeout"`
	// WriteTimeout 写响应的上限；0 表示不限制（大文件/流式接口须显式说明）。
	WriteTimeout time.Duration `yaml:"write_timeout"`
	// IdleTimeout keep-alive 空闲连接上限。
	IdleTimeout time.Duration `yaml:"idle_timeout"`
	// MaxHeaderBytes 请求头总字节上限；0 表示使用标准库默认值。
	MaxHeaderBytes int `yaml:"max_header_bytes"`
	// Timeout 单请求 context deadline（软超时，中间件注入）；0 表示不注入。
	Timeout time.Duration `yaml:"timeout"`
	// BodyLimitBytes 请求体上限；≤0 表示不限制（文件上传接口须显式放大）。
	BodyLimitBytes int64 `yaml:"body_limit_bytes"`
	// TrustedProxies 可信代理 CIDR 列表；只有来自这些地址的请求才解析 X-Forwarded-For。
	// 为空表示不信任任何代理头，直接使用 RemoteAddr（IDN-P1-04）。
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// RateLimitConfig 入口限流（REL-05）；按调用方维度分桶（见 middleware.DefaultRateLimitKey）。
type RateLimitConfig struct {
	// Enabled 是否启用；默认关闭，按端开启。
	Enabled bool `yaml:"enabled"`
	// Driver memory | redis；prod/staging 启用时必须 redis（否则配额被副本数放大）。
	Driver string `yaml:"driver"`
	// Key 分键策略：channel | channel_user | channel_tenant_user | client。
	Key string `yaml:"key"`
	// FailureMode Redis 故障策略：fail_open（默认）| fail_closed。
	FailureMode string `yaml:"failure_mode"`
	// RPS 每秒补充令牌数；须 >0。
	RPS float64 `yaml:"rps"`
	// Burst 桶容量；须 ≥1。
	Burst int `yaml:"burst"`
	// IdleTTL 空闲桶回收阈值；0 时使用 10m。
	IdleTTL time.Duration `yaml:"idle_ttl"`
}

// CORSConfig 跨域配置；生产须显式 allow_origins，禁止反射任意 Origin。
type CORSConfig struct {
	// AllowOrigins 允许的 Origin；为空表示不启用 CORS 中间件。
	AllowOrigins []string `yaml:"allow_origins"`
	// AllowHeaders 允许的请求头；空时使用 pkg/middleware 默认值。
	AllowHeaders []string `yaml:"allow_headers"`
	// ExposeHeaders 暴露给浏览器的响应头。
	ExposeHeaders []string `yaml:"expose_headers"`
	// AllowCredentials 是否允许 Cookie；与 "*" 互斥。
	AllowCredentials bool `yaml:"allow_credentials"`
	// MaxAge 预检缓存时长；≤0 不写该响应头。
	MaxAge time.Duration `yaml:"max_age"`
}

// TracingConfig OpenTelemetry 追踪（OBS）；装配见 pkg/tracing。
type TracingConfig struct {
	// Enabled 为 false 时使用 noop（不生成有效 TraceID）；nil 表示默认 true。
	Enabled *bool `yaml:"enabled"`
	// Exporter：none（默认，只生成 ID）/ otlp / stdout / noop。
	Exporter string `yaml:"exporter"`
	// Endpoint OTLP/HTTP 地址（exporter=otlp 时必填），如 localhost:4318。
	Endpoint string `yaml:"endpoint"`
	// SampleRatio 头采样 [0,1]；nil 表示 1.0。
	SampleRatio *float64 `yaml:"sample_ratio"`
	// Insecure OTLP 明文（无 TLS）；仅限本地/内网。
	Insecure bool `yaml:"insecure"`
}

// IsEnabled 报告追踪是否启用；未配置时默认 true。
func (t TracingConfig) IsEnabled() bool {
	if t.Enabled == nil {
		return true
	}
	return *t.Enabled
}

// LogConfig 日志级别与格式。
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// AuthConfig 鉴权相关配置（含演示模式与 JWT）。
type AuthConfig struct {
	// Mode 鉴权模式：demo 或 jwt；未声明时按环境回退（见 effectiveMode）。
	Mode string `yaml:"mode"`
	// ExpectedChannel 期望端别；须与进程 channel 一致（缺省时由 normalize 派生）。
	ExpectedChannel string `yaml:"expected_channel"`
	// JWT 真实令牌校验参数；mode=jwt 时生效。
	JWT security.JWTConfig `yaml:"jwt"`
}

// effectiveMode 返回生效的鉴权模式。
//
// 未显式声明 mode 时的回退规则：
//   - prod / staging → jwt（缺少 JWT 配置会直接启动失败，绝不静默回退 demo）；
//   - 其余环境（local/dev/test/未声明）→ demo，便于本地起服务。
func (a AuthConfig) effectiveMode(env string) string {
	if mode := strings.ToLower(strings.TrimSpace(a.Mode)); mode != "" {
		return mode
	}
	if isProdLikeEnv(env) {
		return AuthModeJWT
	}
	return AuthModeDemo
}

// DBConfig 数据源驱动与连接串。
type DBConfig struct {
	Driver string `yaml:"driver"` // memory | mysql | postgres
	DSN    string `yaml:"dsn"`
}

// LogValue 实现 slog.LogValuer：DSN 只保留 scheme，避免凭据进日志（SEC-05）。
func (c DBConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("driver", c.Driver),
		slog.String("dsn", redactDSN(c.DSN)),
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
		),
	)
}

// oauthProviderNames 返回 provider 名称列表，供日志输出（不含任何密钥）。
func oauthProviderNames(providers []IdentityOAuthProviderConfig) []string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.Name)
	}
	return names
}

// maskIfSet 在取值非空时返回占位符，避免泄露“密钥是否存在”以外的信息。
func maskIfSet(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return maskedValue
}

// redactDSN 只保留 DSN 的 scheme 部分，其余替换为占位符。
func redactDSN(dsn string) string {
	s := strings.TrimSpace(dsn)
	if s == "" {
		return ""
	}
	if idx := strings.Index(s, "://"); idx >= 0 {
		return s[:idx+3] + maskedValue
	}
	return maskedValue
}

// isProdEnv 报告环境是否为生产。
func isProdEnv(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), "prod")
}

// isProdLikeEnv 报告环境是否按生产标准校验（prod / staging）。
func isProdLikeEnv(env string) bool {
	return isProdEnv(env) || strings.EqualFold(strings.TrimSpace(env), "staging")
}

// NewConfigLoader 构造进程配置加载器。
//
// 当前所有进程只在启动期加载一次（LoadConfig）；pkg/config.Watcher 的热更新
// 能力为预留项，尚未接入任何 cmd。启用前须先明确可热更新字段白名单并写 ADR
// （CONFIG_STANDARDS §9）。保持导出是为了让未来的长驻进程复用同一套校验。
func NewConfigLoader(path string) (*config.Loader[Config], error) {
	return config.NewLoader(config.Options[Config]{
		Files:     []string{path},
		EnvPrefix: envPrefix,
		Defaults:  (*Config).applyDefaults,
		Normalize: (*Config).normalize,
		Validate:  (*Config).Validate,
	})
}

// LoadConfig 从 YAML 文件加载配置，应用默认值与环境变量覆盖后校验。
//
// 优先级：环境变量 > 配置文件 > 默认值（CONFIG_STANDARDS CFG-04）。
// 环境变量无法解析（如非法 duration/bool）时返回错误，不再静默忽略；
// 错误只包含字段路径与变量名，不含取值（SEC-05）。
func LoadConfig(path string) (*Config, error) {
	loader, err := NewConfigLoader(path)
	if err != nil {
		return nil, err
	}
	return loader.Load()
}

// dbOnlyConfig 是 LoadDBConfig 的解码目标，只声明 db 段。
type dbOnlyConfig struct {
	DB DBConfig `yaml:"db"`
}

// LoadDBConfig 仅加载配置文件的 db 段，并应用 ADMIN_FRAME_DB_* 环境变量覆盖。
//
// 供 cmd/migrate 等只需要数据库连接的工具复用 CFG-04 的配置优先级；
// 不校验服务级字段（service/channel/addr）。path 为空时只读取环境变量。
// 传入完整服务配置时其余字段被忽略（AllowUnknownFields），便于直接复用线上 yaml。
func LoadDBConfig(path string) (DBConfig, error) {
	opts := config.Options[dbOnlyConfig]{
		EnvPrefix:          envPrefix,
		AllowUnknownFields: true,
	}
	if path != "" {
		opts.Files = []string{path}
	}
	loader, err := config.NewLoader(opts)
	if err != nil {
		return DBConfig{}, err
	}
	cfg, err := loader.Load()
	if err != nil {
		return DBConfig{}, err
	}
	return cfg.DB, nil
}

// applyDefaults 在解码文件前填充默认值。
//
// 只处理与其它字段无关的常量默认值；跨字段派生放在 normalize，
// 以免被文件或环境变量覆盖后失去一致性。
func (c *Config) applyDefaults() {
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 15 * time.Second
	}
	if c.HTTP.ReadHeaderTimeout == 0 {
		c.HTTP.ReadHeaderTimeout = 5 * time.Second
	}
	if c.HTTP.ReadTimeout == 0 {
		c.HTTP.ReadTimeout = 15 * time.Second
	}
	if c.HTTP.WriteTimeout == 0 {
		c.HTTP.WriteTimeout = 30 * time.Second
	}
	if c.HTTP.IdleTimeout == 0 {
		c.HTTP.IdleTimeout = 60 * time.Second
	}
	if c.HTTP.MaxHeaderBytes == 0 {
		c.HTTP.MaxHeaderBytes = 1 << 20
	}
	if c.HTTP.Timeout == 0 {
		c.HTTP.Timeout = 30 * time.Second
	}
	if c.HTTP.BodyLimitBytes == 0 {
		c.HTTP.BodyLimitBytes = 32 << 20
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	if c.DB.Driver == "" {
		c.DB.Driver = "memory"
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "memory"
	}
	if c.Worker.PollInterval == 0 {
		c.Worker.PollInterval = 5 * time.Second
	}
	if c.Tracing.Exporter == "" {
		c.Tracing.Exporter = "none"
	}
	if c.ID.NodeIDSource == "" {
		c.ID.NodeIDSource = IDSourceEnv
	}
	if c.Idempotency.Driver == "" {
		c.Idempotency.Driver = IdempotencyDriverMemory
	}
	if c.Idempotency.Header == "" {
		c.Idempotency.Header = "Idempotency-Key"
	}
	if c.Idempotency.TTL == 0 {
		c.Idempotency.TTL = 24 * time.Hour
	}
	if c.Idempotency.MaxBodyBytes == 0 {
		c.Idempotency.MaxBodyBytes = 1 << 20
	}
	if c.RateLimit.Driver == "" {
		c.RateLimit.Driver = RateLimitDriverMemory
	}
	if c.RateLimit.Key == "" {
		c.RateLimit.Key = RateLimitKeyChannel
	}
	if c.RateLimit.FailureMode == "" {
		c.RateLimit.FailureMode = RateLimitFailureOpen
	}
	if c.RateLimit.IdleTTL == 0 {
		c.RateLimit.IdleTTL = 10 * time.Minute
	}
	c.applyAsyncDefaults()
	c.applyIAMDefaults()
	c.applyIdentityDefaults()
}

// applyIdentityDefaults 填充身份域默认值；Enabled 保持 false（默认关闭）。
func (c *Config) applyIdentityDefaults() {
	if c.Identity.AccessTTL == 0 {
		c.Identity.AccessTTL = 15 * time.Minute
	}
	if c.Identity.RefreshTTL == 0 {
		c.Identity.RefreshTTL = 168 * time.Hour
	}
	if c.Identity.Session.IdleTimeout == 0 {
		c.Identity.Session.IdleTimeout = 72 * time.Hour
	}
	if c.Identity.Session.AbsoluteTTL == 0 {
		c.Identity.Session.AbsoluteTTL = 720 * time.Hour
	}
	if c.Identity.Session.MaxPerAccount == 0 {
		c.Identity.Session.MaxPerAccount = 10
	}
	if c.Identity.Session.RefreshGrace == 0 {
		c.Identity.Session.RefreshGrace = 10 * time.Second
	}
	if c.Identity.Retention.SessionDays == 0 {
		c.Identity.Retention.SessionDays = 30
	}
	if c.Identity.Retention.LoginAttemptDays == 0 {
		c.Identity.Retention.LoginAttemptDays = 180
	}
	// 0 使用默认 365 天；负数表示显式关闭归档。
	if c.Identity.Retention.ArchiveDays == 0 {
		c.Identity.Retention.ArchiveDays = 365
	}
	if c.Identity.Retention.CleanupInterval == 0 {
		c.Identity.Retention.CleanupInterval = time.Hour
	}
	if c.Identity.Revocation.Driver == "" {
		c.Identity.Revocation.Driver = IdentityRevocationDriverRedis
	}
	if c.Identity.Revocation.FailureMode == "" {
		c.Identity.Revocation.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.Revocation.StateTTL == 0 {
		// 默认 min(access_ttl + 1m, 60s)：常规 15m access_ttl 下即 60s。
		accessLeeway := c.Identity.AccessTTL + time.Minute
		if accessLeeway > identityMaxRevocationStateTTL {
			accessLeeway = identityMaxRevocationStateTTL
		}
		c.Identity.Revocation.StateTTL = accessLeeway
	}
	// 负数表示显式关闭 L1；0 使用默认 1s，避免「未配置」被误读为关闭。
	if c.Identity.Revocation.LocalCacheTTL == 0 {
		c.Identity.Revocation.LocalCacheTTL = identityDefaultRevocationLocalCacheTTL
	}
	if c.Identity.Revocation.LocalCacheCapacity <= 0 {
		c.Identity.Revocation.LocalCacheCapacity = identityDefaultRevocationLocalCacheCapacity
	}
	if c.Identity.Lockout.MaxFailures == 0 {
		c.Identity.Lockout.MaxFailures = 5
	}
	if c.Identity.Lockout.Window == 0 {
		c.Identity.Lockout.Window = 15 * time.Minute
	}
	if c.Identity.Lockout.Duration == 0 {
		c.Identity.Lockout.Duration = 15 * time.Minute
	}
	if c.Identity.LoginRateLimit.RPS == 0 {
		c.Identity.LoginRateLimit.RPS = 1
	}
	if c.Identity.LoginRateLimit.Burst == 0 {
		c.Identity.LoginRateLimit.Burst = 5
	}
	if c.Identity.LoginRateLimit.FailureMode == "" {
		c.Identity.LoginRateLimit.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.LoginRateLimit.IPRPS == 0 {
		c.Identity.LoginRateLimit.IPRPS = 20
	}
	if c.Identity.LoginRateLimit.IPBurst == 0 {
		c.Identity.LoginRateLimit.IPBurst = 40
	}
	if c.Identity.RefreshRateLimit.RPS == 0 {
		c.Identity.RefreshRateLimit.RPS = 10
	}
	if c.Identity.RefreshRateLimit.Burst == 0 {
		c.Identity.RefreshRateLimit.Burst = 20
	}
	if c.Identity.RefreshRateLimit.FailureMode == "" {
		c.Identity.RefreshRateLimit.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.HashKeyID == "" && strings.TrimSpace(c.Identity.HashKey) != "" {
		c.Identity.HashKeyID = "v1"
	}
	if c.Identity.Password.WaitTimeout == 0 {
		c.Identity.Password.WaitTimeout = identityDefaultPasswordWaitTimeout
	}
	if c.Identity.Password.HistoryCount == 0 {
		c.Identity.Password.HistoryCount = 5
	}
	if c.Identity.Password.HistoryWindow == 0 {
		c.Identity.Password.HistoryWindow = 365 * 24 * time.Hour
	}
	if c.Identity.OAuth.RateLimit.RPS == 0 {
		c.Identity.OAuth.RateLimit.RPS = 1
	}
	if c.Identity.OAuth.RateLimit.Burst == 0 {
		c.Identity.OAuth.RateLimit.Burst = 5
	}
	if c.Identity.OAuth.RateLimit.FailureMode == "" {
		c.Identity.OAuth.RateLimit.FailureMode = RateLimitFailureClosed
	}
	if c.Identity.OAuth.Timeout == 0 {
		c.Identity.OAuth.Timeout = 5 * time.Second
	}
	if c.Identity.OAuth.BreakerFailures == 0 {
		c.Identity.OAuth.BreakerFailures = 5
	}
	if c.Identity.OAuth.BreakerTimeout == 0 {
		c.Identity.OAuth.BreakerTimeout = 60 * time.Second
	}
	if c.Identity.MFA.Issuer == "" {
		c.Identity.MFA.Issuer = identityDefaultMFAIssuer
	}
	if c.Identity.SMS.Driver == "" {
		c.Identity.SMS.Driver = IdentitySMSDriverMemory
	}
	if c.Identity.SMS.CodeTTL == 0 {
		c.Identity.SMS.CodeTTL = 5 * time.Minute
	}
	if c.Identity.SMS.MaxAttempts == 0 {
		c.Identity.SMS.MaxAttempts = 5
	}
	if c.Identity.SMS.ResendInterval == 0 {
		c.Identity.SMS.ResendInterval = time.Minute
	}
	if c.Identity.SMS.DailyPerPhone == 0 {
		c.Identity.SMS.DailyPerPhone = 10
	}
	if c.Identity.SMS.HourlyPerIP == 0 {
		c.Identity.SMS.HourlyPerIP = 30
	}
	if c.Identity.SMS.BreakerFailures == 0 {
		c.Identity.SMS.BreakerFailures = identityDefaultSMSBreakerFailures
	}
	if c.Identity.SMS.BreakerTimeout == 0 {
		c.Identity.SMS.BreakerTimeout = identityDefaultSMSBreakerTimeout
	}
	for i := range c.Identity.OAuth.Providers {
		if strings.TrimSpace(c.Identity.OAuth.Providers[i].SubjectField) == "" {
			c.Identity.OAuth.Providers[i].SubjectField = "sub"
		}
	}
}

// applyAsyncDefaults 填充异步运行时默认值（默认关闭，保持现有行为）。
func (c *Config) applyAsyncDefaults() {
	if c.Async.Driver == "" {
		c.Async.Driver = AsyncDriverDB
	}
	if c.Async.Outbox.PollInterval == 0 {
		c.Async.Outbox.PollInterval = time.Second
	}
	if c.Async.Outbox.BatchSize == 0 {
		c.Async.Outbox.BatchSize = 100
	}
	if c.Async.Outbox.Lease == 0 {
		c.Async.Outbox.Lease = 30 * time.Second
	}
	if c.Async.Outbox.MaxAttempts == 0 {
		c.Async.Outbox.MaxAttempts = 10
	}
	if c.Async.Outbox.Concurrency == 0 {
		c.Async.Outbox.Concurrency = 2
	}
	if c.Async.Consumer.Concurrency == 0 {
		c.Async.Consumer.Concurrency = 4
	}
	if c.Async.Consumer.VisibilityTimeout == 0 {
		c.Async.Consumer.VisibilityTimeout = 60 * time.Second
	}
	if c.Async.Consumer.RetryBackoff == "" {
		c.Async.Consumer.RetryBackoff = AsyncBackoffExponential
	}
	if c.Async.Broker.Stream == "" {
		c.Async.Broker.Stream = "async"
	}
	if c.Async.Broker.SubjectPrefix == "" {
		c.Async.Broker.SubjectPrefix = "async"
	}
	if c.Async.Broker.AckWait == 0 {
		c.Async.Broker.AckWait = 30 * time.Second
	}
	if c.Async.Broker.MaxDeliver == 0 {
		c.Async.Broker.MaxDeliver = 10
	}
}

// applyIAMDefaults 填充 IAM 默认值；Mode 保持空串，由 normalize 统一为 off。
func (c *Config) applyIAMDefaults() {
	if c.IAM.LoadTimeout == 0 {
		c.IAM.LoadTimeout = 300 * time.Millisecond
	}
	if c.IAM.Cache.Driver == "" {
		c.IAM.Cache.Driver = IAMCacheDriverMemory
	}
	if c.IAM.Cache.L1TTL == 0 {
		c.IAM.Cache.L1TTL = 3 * time.Second
	}
	if c.IAM.Cache.L2TTL == 0 {
		c.IAM.Cache.L2TTL = 5 * time.Minute
	}
	if c.IAM.Cache.NegativeTTL == 0 {
		c.IAM.Cache.NegativeTTL = 30 * time.Second
	}
	if c.IAM.Cache.KeyPrefix == "" {
		c.IAM.Cache.KeyPrefix = "iam"
	}
	if c.IAM.Cache.L1MaxEntries == 0 {
		c.IAM.Cache.L1MaxEntries = 10000
	}
	if c.IAM.Limits.MaxRolesPerSubject == 0 {
		c.IAM.Limits.MaxRolesPerSubject = 20
	}
	if c.IAM.Limits.MaxPermissionsPerRole == 0 {
		c.IAM.Limits.MaxPermissionsPerRole = 200
	}
	if c.IAM.Limits.MaxBindingsPerSubject == 0 {
		c.IAM.Limits.MaxBindingsPerSubject = 50
	}
	if c.IAM.Limits.MaxCustomRolesPerMerchant == 0 {
		c.IAM.Limits.MaxCustomRolesPerMerchant = 50
	}
}

// normalize 在环境变量覆盖后、Validate 前补齐跨字段派生值。
//
// auth.expected_channel 未显式声明时跟随 channel，保证环境变量改写
// channel 后两者仍然一致；auth.mode 未声明时按环境回退（CFG-04）。
func (c *Config) normalize() {
	if c.Auth.ExpectedChannel == "" {
		c.Auth.ExpectedChannel = c.Channel
	}
	c.Auth.Mode = c.Auth.effectiveMode(c.Env)
	c.IAM.Mode = normalizeIAMMode(c.IAM.Mode)
	c.IAM.Constraints.Mode = normalizeIAMConstraintMode(c.IAM.Constraints.Mode)
}

// normalizeIAMConstraintMode 归一化 RBAC2 约束模式；空串视为 shadow（影子期默认）。
func normalizeIAMConstraintMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return IAMConstraintsModeShadow
	}
	return mode
}

// normalizeIAMMode 归一化 iam.mode；空串视为 off（保持历史静态权限表行为）。
func normalizeIAMMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return IAMModeOff
	}
	return mode
}

// Validate 校验必填项、枚举与生产互斥项（CFG-03/05）。
//
// 错误通过 errors.Join 聚合，启动日志可一次列出全部问题；
// 错误文本只包含字段路径，不回显取值。
func (c *Config) Validate() error {
	var errs []error
	if c.Service == "" {
		errs = append(errs, errors.New("service is required"))
	}
	if c.Channel == "" {
		errs = append(errs, errors.New("channel is required"))
	} else {
		switch c.Channel {
		case "admin", "biz", "c", "worker":
		default:
			errs = append(errs, fmt.Errorf("unsupported channel: %s", c.Channel))
		}
	}
	if c.Addr == "" {
		errs = append(errs, errors.New("addr is required"))
	}
	if c.Auth.ExpectedChannel != c.Channel {
		errs = append(errs, fmt.Errorf("auth.expected_channel %q must equal channel %q", c.Auth.ExpectedChannel, c.Channel))
	}
	errs = append(errs, c.validateLog()...)
	errs = append(errs, c.validateHTTP()...)
	errs = append(errs, c.validateAuth()...)
	errs = append(errs, c.validateDB()...)
	if err := ValidateStorage(c.Env, c.Storage); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, c.validateEnv()...)
	errs = append(errs, c.validateTracing()...)
	errs = append(errs, c.validateID()...)
	errs = append(errs, c.validateIdempotency()...)
	errs = append(errs, c.validateRateLimit()...)
	errs = append(errs, c.validateIAM()...)
	errs = append(errs, c.validateIdentity()...)
	return errors.Join(errs...)
}

// validateLog 校验日志级别与格式；prod 禁止 text（OBS-01）。
func (c *Config) validateLog() []error {
	var errs []error
	switch strings.ToLower(strings.TrimSpace(c.Log.Level)) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("unsupported log.level: %s", c.Log.Level))
	}
	switch strings.ToLower(strings.TrimSpace(c.Log.Format)) {
	case "json", "text":
	default:
		// 必须在启动前拦截：NewLogger 使用 logger.Must，非法格式会 panic。
		errs = append(errs, fmt.Errorf("unsupported log.format: %s", c.Log.Format))
	}
	if isProdEnv(c.Env) && strings.EqualFold(strings.TrimSpace(c.Log.Format), "text") {
		errs = append(errs, errors.New("log.format=text is forbidden when env=prod"))
	}
	return errs
}

// validateHTTP 校验 Server 加固参数与请求链路参数（REL-01/02）。
func (c *Config) validateHTTP() []error {
	var errs []error
	if c.HTTP.ReadHeaderTimeout < 0 {
		errs = append(errs, errors.New("http.read_header_timeout must be >= 0"))
	}
	if c.HTTP.MaxHeaderBytes < 0 {
		errs = append(errs, errors.New("http.max_header_bytes must be >= 0"))
	}
	if c.HTTP.Timeout < 0 {
		errs = append(errs, errors.New("http.timeout must be >= 0"))
	}
	if isProdEnv(c.Env) && c.HTTP.Timeout <= 0 {
		errs = append(errs, errors.New("http.timeout must be > 0 when env=prod"))
	}
	if c.HTTP.BodyLimitBytes < 0 {
		errs = append(errs, errors.New("http.body_limit_bytes must be >= 0"))
	}
	for _, raw := range c.HTTP.TrustedProxies {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(strings.TrimSpace(raw)); err != nil {
			errs = append(errs, errors.New("http.trusted_proxies must be valid CIDR"))
		}
	}
	return errs
}

// validateAuth 校验鉴权模式、端别隔离与 JWT 配置（SEC-04 / CFG-05）。
func (c *Config) validateAuth() []error {
	var errs []error
	mode := c.Auth.effectiveMode(c.Env)
	switch mode {
	case AuthModeDemo, AuthModeJWT:
	default:
		errs = append(errs, fmt.Errorf("unsupported auth.mode: %s", c.Auth.Mode))
	}
	if mode == AuthModeDemo && isProdEnv(c.Env) {
		errs = append(errs, errors.New("auth.mode=demo is forbidden when env=prod"))
	}
	if mode == AuthModeJWT {
		if err := c.Auth.JWT.Validate(c.Channel); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// validateDB 校验驱动枚举与 DSN 必填。
func (c *Config) validateDB() []error {
	var errs []error
	switch c.DB.Driver {
	case "memory", "mysql", "postgres":
	default:
		errs = append(errs, fmt.Errorf("unsupported db.driver: %s", c.DB.Driver))
		return errs
	}
	if c.DB.Driver != "memory" && c.DB.DSN == "" {
		errs = append(errs, fmt.Errorf("db.dsn is required when driver is %s", c.DB.Driver))
	}
	return errs
}

// validateEnv 校验环境枚举与生产互斥项。
func (c *Config) validateEnv() []error {
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "", "local", "dev", "test", "staging", "prod":
	default:
		return []error{fmt.Errorf("unsupported env: %s", c.Env)}
	}
	return nil
}

// validateTracing 校验 exporter、endpoint 与生产互斥项（OBS）。
func (c *Config) validateTracing() []error {
	var errs []error
	switch strings.ToLower(strings.TrimSpace(c.Tracing.Exporter)) {
	case "", "none", "otlp", "stdout", "noop":
	default:
		errs = append(errs, fmt.Errorf("unsupported tracing.exporter: %s", c.Tracing.Exporter))
	}
	if c.Tracing.IsEnabled() && strings.EqualFold(c.Tracing.Exporter, "otlp") && strings.TrimSpace(c.Tracing.Endpoint) == "" {
		errs = append(errs, errors.New("tracing.endpoint is required when exporter=otlp"))
	}
	if c.Tracing.SampleRatio != nil {
		if r := *c.Tracing.SampleRatio; r < 0 || r > 1 {
			errs = append(errs, errors.New("tracing.sample_ratio must be in [0,1]"))
		}
	}
	if isProdEnv(c.Env) && strings.EqualFold(c.Tracing.Exporter, "stdout") {
		errs = append(errs, errors.New("tracing.exporter=stdout is forbidden when env=prod"))
	}
	return errs
}

// validateID 校验 NodeID 来源、范围与生产必填项（ENT-02 T1）。
func (c *Config) validateID() []error {
	var errs []error
	source := strings.ToLower(strings.TrimSpace(c.ID.NodeIDSource))
	switch source {
	case "", IDSourceConfig, IDSourceEnv:
	case IDSourceStatefulSet:
		if c.ID.NodeID != nil {
			errs = append(errs, errors.New("id.node_id must not be set when id.node_id_source=statefulset"))
		}
	case "registry":
		errs = append(errs, errors.New("id.node_id_source=registry is not supported"))
	default:
		errs = append(errs, fmt.Errorf("unsupported id.node_id_source: %s", c.ID.NodeIDSource))
	}
	if c.ID.NodeID != nil && (*c.ID.NodeID < 0 || *c.ID.NodeID > maxNodeID) {
		errs = append(errs, fmt.Errorf("id.node_id must be in [0, %d]", maxNodeID))
	}
	if isProdLikeEnv(c.Env) && c.Channel != "worker" &&
		source != IDSourceStatefulSet && c.ID.NodeID == nil {
		errs = append(errs, errors.New("id.node_id is required in prod/staging when id.node_id_source is not statefulset"))
	}
	return errs
}

// validateIdempotency 校验幂等驱动、TTL 与生产强制项（ENT-02 T3）。
func (c *Config) validateIdempotency() []error {
	if !c.Idempotency.Enabled {
		return nil
	}
	var errs []error
	driver := strings.ToLower(strings.TrimSpace(c.Idempotency.Driver))
	switch driver {
	case "", IdempotencyDriverMemory:
	case IdempotencyDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when idempotency.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported idempotency.driver: %s", c.Idempotency.Driver))
	}
	if c.Idempotency.TTL <= 0 {
		errs = append(errs, errors.New("idempotency.ttl must be > 0 when idempotency.enabled=true"))
	}
	if strings.TrimSpace(c.Idempotency.Header) == "" {
		errs = append(errs, errors.New("idempotency.header is required when idempotency.enabled=true"))
	}
	if c.Idempotency.MaxBodyBytes < 0 {
		errs = append(errs, errors.New("idempotency.max_body_bytes must be >= 0"))
	}
	if isProdLikeEnv(c.Env) && driver != IdempotencyDriverRedis {
		errs = append(errs, errors.New("idempotency.driver must be redis when enabled in prod/staging"))
	}
	return errs
}

// validateRateLimit 校验限流驱动、分键、失败策略与参数（REL-05 / ENT-02 T5-T6）。
func (c *Config) validateRateLimit() []error {
	if !c.RateLimit.Enabled {
		return nil
	}
	var errs []error
	driver := strings.ToLower(strings.TrimSpace(c.RateLimit.Driver))
	switch driver {
	case "", RateLimitDriverMemory:
	case RateLimitDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when rate_limit.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported rate_limit.driver: %s", c.RateLimit.Driver))
	}
	switch strings.ToLower(strings.TrimSpace(c.RateLimit.Key)) {
	case "", RateLimitKeyChannel, RateLimitKeyChannelUser, RateLimitKeyChannelTenantUser, RateLimitKeyClient:
	default:
		errs = append(errs, fmt.Errorf("unsupported rate_limit.key: %s", c.RateLimit.Key))
	}
	switch strings.ToLower(strings.TrimSpace(c.RateLimit.FailureMode)) {
	case "", RateLimitFailureOpen, RateLimitFailureClosed:
	default:
		errs = append(errs, fmt.Errorf("unsupported rate_limit.failure_mode: %s", c.RateLimit.FailureMode))
	}
	if c.RateLimit.RPS <= 0 {
		errs = append(errs, errors.New("rate_limit.rps must be > 0 when rate_limit.enabled=true"))
	}
	if c.RateLimit.Burst < 1 {
		errs = append(errs, errors.New("rate_limit.burst must be >= 1 when rate_limit.enabled=true"))
	}
	if c.RateLimit.IdleTTL < 0 {
		errs = append(errs, errors.New("rate_limit.idle_ttl must be >= 0"))
	}
	if isProdLikeEnv(c.Env) && driver != RateLimitDriverRedis {
		errs = append(errs, errors.New("rate_limit.driver must be redis when enabled in prod/staging"))
	}
	return errs
}

// validateIAM 校验 IAM 模式、缓存 TTL、生产互斥项与端别约束（docs/rbac-iam.md §12.2）。
//
// 错误只包含字段路径与约束，不回显取值（CFG-05）。
func (c *Config) validateIAM() []error {
	// 完全未配置 iam 段时保持历史行为（zero-value Config 直接调用 Validate 也不会误报）；
	// 生产路径统一经 LoadConfig → applyDefaults，因此段内字段必然已填充。
	if c.IAM.Mode == "" && c.IAM.Cache.Driver == "" && c.IAM.LoadTimeout == 0 && !c.IAM.Approval.Enabled {
		return nil
	}
	var errs []error
	mode := normalizeIAMMode(c.IAM.Mode)
	switch mode {
	case IAMModeOff, IAMModeShadow, IAMModeEnforce:
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.mode: %s", c.IAM.Mode))
	}

	if c.Service != "" {
		// C 端与 worker 不挂载 IAM；显式开启会在无 transport 时静默失效，故启动即拒绝。
		if (c.Channel == "c" || c.Channel == "worker") && mode != IAMModeOff {
			errs = append(errs, fmt.Errorf("iam.mode must be off when channel is %s", c.Channel))
		}
	}

	if c.IAM.LoadTimeout <= 0 || c.IAM.LoadTimeout > time.Second {
		errs = append(errs, errors.New("iam.load_timeout must be in (0, 1s]"))
	}
	switch c.IAM.Cache.Driver {
	case IAMCacheDriverMemory:
	case IAMCacheDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when iam.cache.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.cache.driver: %s", c.IAM.Cache.Driver))
	}
	if c.IAM.Cache.L1TTL <= 0 || c.IAM.Cache.L1TTL > 5*time.Second {
		errs = append(errs, errors.New("iam.cache.l1_ttl must be in (0, 5s]"))
	}
	if c.IAM.Cache.L2TTL <= 0 || c.IAM.Cache.L2TTL > 15*time.Minute {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be in (0, 15m]"))
	}
	if c.IAM.Cache.L2TTL <= c.IAM.Cache.L1TTL {
		errs = append(errs, errors.New("iam.cache.l2_ttl must be greater than iam.cache.l1_ttl"))
	}
	if c.IAM.Cache.NegativeTTL <= 0 || c.IAM.Cache.NegativeTTL > c.IAM.Cache.L2TTL {
		errs = append(errs, errors.New("iam.cache.negative_ttl must be in (0, iam.cache.l2_ttl]"))
	}
	if c.IAM.Cache.L1MaxEntries < 0 {
		errs = append(errs, errors.New("iam.cache.l1_max_entries must be >= 0"))
	}
	if c.IAM.Limits.MaxRolesPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_roles_per_subject must be > 0"))
	}
	if c.IAM.Limits.MaxPermissionsPerRole <= 0 {
		errs = append(errs, errors.New("iam.limits.max_permissions_per_role must be > 0"))
	}
	if c.IAM.Limits.MaxBindingsPerSubject <= 0 {
		errs = append(errs, errors.New("iam.limits.max_bindings_per_subject must be > 0"))
	}
	if c.Channel == "biz" && c.IAM.Limits.MaxCustomRolesPerMerchant <= 0 {
		errs = append(errs, errors.New("iam.limits.max_custom_roles_per_merchant must be > 0 when channel=biz"))
	}
	// 空串在 normalize 中会归一为 shadow；校验阶段同样视为合法，避免直接构造配置时误报。
	switch c.IAM.Constraints.Mode {
	case "", IAMConstraintsModeOff, IAMConstraintsModeShadow, IAMConstraintsModeEnforce:
	default:
		errs = append(errs, fmt.Errorf("unsupported iam.constraints.mode: %s", c.IAM.Constraints.Mode))
	}

	if isProdLikeEnv(c.Env) && mode == IAMModeEnforce && c.IAM.Cache.Driver != IAMCacheDriverRedis {
		errs = append(errs, errors.New("iam.cache.driver must be redis when iam.mode=enforce in prod/staging"))
	}
	if isProdEnv(c.Env) && c.IAM.Security.AllowWildcard {
		errs = append(errs, errors.New("iam.security.allow_wildcard must be false when env=prod"))
	}
	if c.IAM.Breakglass.Enabled {
		if c.Channel != "admin" {
			errs = append(errs, errors.New("iam.breakglass.enabled is only supported on channel=admin"))
		}
		if mode != IAMModeEnforce {
			errs = append(errs, errors.New("iam.breakglass.enabled requires iam.mode=enforce"))
		}
		if isProdEnv(c.Env) && len(c.IAM.Breakglass.AllowedSubjects) == 0 {
			errs = append(errs, errors.New("iam.breakglass.allowed_subjects is required when env=prod"))
		}
		for i, subject := range c.IAM.Breakglass.AllowedSubjects {
			if strings.TrimSpace(subject) == "" {
				errs = append(errs, fmt.Errorf("iam.breakglass.allowed_subjects[%d] must not be empty", i))
			}
		}
	}
	if c.IAM.Approval.Enabled {
		// 审批单属于 A / B 端治理能力；C 端与 worker 没有 IAM transport。
		if c.Channel == "c" || c.Channel == "worker" {
			errs = append(errs, errors.New("iam.approval.enabled is only supported on channel=admin or biz"))
		}
		// 待审批单必须跨重启保留：内存仓储只用于 local / demo / 单测。
		if isProdEnv(c.Env) && c.DB.Driver == "memory" {
			errs = append(errs, errors.New("iam.approval.enabled requires a persistent db.driver when env=prod"))
		}
	}
	if isProdLikeEnv(c.Env) && mode == IAMModeEnforce && !c.Async.Enabled {
		errs = append(errs, errors.New("async.enabled must be true when iam.mode=enforce in prod/staging"))
	}
	errs = append(errs, c.validateAsync()...)
	return errs
}

// validHashKeyID 报告 key id 是否仅含字母 / 数字 / 下划线 / 短横线且长度 ≤16。
func validHashKeyID(id string) bool {
	if id == "" || len(id) > 16 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// validateIdentity 校验身份域开关、TTL、撤销存储与端别约束（docs/identity-authn.md §10.2）。
//
// 未启用时不校验派生字段，保持历史配置可直接启动；错误只包含字段路径。
func (c *Config) validateIdentity() []error {
	if !c.Identity.Enabled {
		return nil
	}
	var errs []error
	if c.Auth.effectiveMode(c.Env) != AuthModeJWT {
		errs = append(errs, errors.New("auth.mode must be jwt when identity.enabled=true"))
	}
	if c.Channel != "admin" && c.Channel != "biz" && c.Channel != "c" {
		// worker 不提供认证 transport；C 端登录自阶段 4 起纳入。
		errs = append(errs, errors.New("identity.enabled is only supported on channel=admin, biz or c"))
	}
	if c.Identity.AccessTTL < 5*time.Minute || c.Identity.AccessTTL > time.Hour {
		errs = append(errs, errors.New("identity.access_ttl must be in [5m, 1h]"))
	}
	if c.Identity.RefreshTTL <= c.Identity.AccessTTL {
		errs = append(errs, errors.New("identity.refresh_ttl must be greater than identity.access_ttl"))
	}
	if c.Identity.Session.IdleTimeout <= 0 || c.Identity.Session.IdleTimeout > c.Identity.RefreshTTL {
		errs = append(errs, errors.New("identity.session.idle_timeout must be in (0, identity.refresh_ttl]"))
	}
	// 0 表示由默认值填充（直接构造 Config 的单测路径），不作为非法值处理。
	if c.Identity.Session.AbsoluteTTL != 0 && c.Identity.Session.AbsoluteTTL <= c.Identity.RefreshTTL {
		errs = append(errs, errors.New("identity.session.absolute_ttl must be greater than identity.refresh_ttl"))
	}
	if c.Identity.Session.MaxPerAccount < 1 {
		errs = append(errs, errors.New("identity.session.max_per_account must be >= 1"))
	}
	// 0 表示由默认值填充（10s）；宽限窗口必须有安全上限。
	if c.Identity.Session.RefreshGrace < 0 || c.Identity.Session.RefreshGrace > 60*time.Second {
		errs = append(errs, errors.New("identity.session.refresh_grace must be in [0, 60s]"))
	}
	// 身份域账号与会话必须跨重启保留并跨副本可见（IDN-P2-01）。
	if isProdLikeEnv(c.Env) && c.DB.Driver == "memory" {
		errs = append(errs, errors.New("db.driver must not be memory when identity.enabled=true in prod/staging"))
	}
	// 生产必须把认证审计落库（Outbox），禁止仅写进程日志（IDN-P2-01）。
	if isProdLikeEnv(c.Env) && !c.Async.Enabled {
		errs = append(errs, errors.New("async.enabled must be true when identity.enabled=true in prod/staging"))
	}
	errs = append(errs, c.validateIdentityPassword()...)
	if c.Identity.Retention.SessionDays < 7 {
		errs = append(errs, errors.New("identity.retention.session_days must be >= 7"))
	}
	if c.Identity.Retention.LoginAttemptDays < 180 {
		errs = append(errs, errors.New("identity.retention.login_attempt_days must be >= 180"))
	}
	// 归档保留期必须覆盖在线保留期；负数表示关闭归档，不参与比较。
	if c.Identity.Retention.ArchiveDays > 0 && c.Identity.Retention.ArchiveDays < c.Identity.Retention.SessionDays {
		errs = append(errs, errors.New("identity.retention.archive_days must be >= session_days (negative disables)"))
	}
	if c.Identity.Retention.CleanupInterval < time.Minute {
		errs = append(errs, errors.New("identity.retention.cleanup_interval must be >= 1m"))
	}
	switch c.Identity.Revocation.Driver {
	case IdentityRevocationDriverRedis:
		if !c.Redis.Enabled() {
			errs = append(errs, errors.New("redis.addr is required when identity.revocation.driver=redis"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.revocation.driver: %s", c.Identity.Revocation.Driver))
	}
	switch c.Identity.Revocation.FailureMode {
	case RateLimitFailureClosed, RateLimitFailureOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.revocation.failure_mode: %s", c.Identity.Revocation.FailureMode))
	}
	if isProdLikeEnv(c.Env) && c.Identity.Revocation.FailureMode == RateLimitFailureOpen {
		errs = append(errs, errors.New("identity.revocation.failure_mode must be fail_closed in prod/staging"))
	}
	// 0 表示由默认值填充（直接构造 Config 的单测路径），不作为非法值处理。
	if c.Identity.Revocation.StateTTL != 0 {
		if c.Identity.Revocation.StateTTL < identityMinRevocationStateTTL {
			errs = append(errs, errors.New("identity.revocation.state_ttl must be >= 1s"))
		}
		if c.Identity.Revocation.StateTTL > identityMaxRevocationStateTTL {
			errs = append(errs, errors.New("identity.revocation.state_ttl must be <= 60s"))
		}
	}
	// 负数表示关闭 L1；正数必须落在 (0, 5s]，避免缓存放大撤销延迟。
	if c.Identity.Revocation.LocalCacheTTL > 0 &&
		c.Identity.Revocation.LocalCacheTTL > identityMaxRevocationLocalCacheTTL {
		errs = append(errs, errors.New("identity.revocation.local_cache_ttl must be <= 5s (negative disables)"))
	}
	if c.Identity.Revocation.LocalCacheCapacity > identityMaxRevocationLocalCacheCapacity {
		errs = append(errs, errors.New("identity.revocation.local_cache_capacity must be <= 100000"))
	}
	if c.Identity.Lockout.MaxFailures < 1 {
		errs = append(errs, errors.New("identity.lockout.max_failures must be >= 1"))
	}
	if c.Identity.Lockout.Window <= 0 {
		errs = append(errs, errors.New("identity.lockout.window must be > 0"))
	}
	if c.Identity.Lockout.Duration <= 0 {
		errs = append(errs, errors.New("identity.lockout.duration must be > 0"))
	}
	if c.Identity.LoginRateLimit.RPS <= 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.rps must be > 0"))
	}
	if c.Identity.LoginRateLimit.Burst < 1 {
		errs = append(errs, errors.New("identity.login_rate_limit.burst must be >= 1"))
	}
	switch c.Identity.LoginRateLimit.FailureMode {
	// 空值表示由默认值填充（直接构造 Config 的单测路径）。
	case "", RateLimitFailureClosed, RateLimitFailureOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.login_rate_limit.failure_mode: %s", c.Identity.LoginRateLimit.FailureMode))
	}
	if isProdLikeEnv(c.Env) && c.Identity.LoginRateLimit.FailureMode == RateLimitFailureOpen {
		errs = append(errs, errors.New("identity.login_rate_limit.failure_mode must be fail_closed in prod/staging"))
	}
	if c.Identity.LoginRateLimit.IPRPS < 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.ip_rps must be >= 0"))
	}
	if c.Identity.LoginRateLimit.IPBurst < 0 {
		errs = append(errs, errors.New("identity.login_rate_limit.ip_burst must be >= 0"))
	}
	// 0 表示由默认值填充（10 rps / burst 20）；仅拒绝负值与非法枚举。
	if c.Identity.RefreshRateLimit.RPS < 0 {
		errs = append(errs, errors.New("identity.refresh_rate_limit.rps must be >= 0"))
	}
	if c.Identity.RefreshRateLimit.Burst < 0 {
		errs = append(errs, errors.New("identity.refresh_rate_limit.burst must be >= 0"))
	}
	switch c.Identity.RefreshRateLimit.FailureMode {
	case "", RateLimitFailureClosed, RateLimitFailureOpen:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.refresh_rate_limit.failure_mode: %s", c.Identity.RefreshRateLimit.FailureMode))
	}
	if isProdLikeEnv(c.Env) && c.Identity.RefreshRateLimit.FailureMode == RateLimitFailureOpen {
		errs = append(errs, errors.New("identity.refresh_rate_limit.failure_mode must be fail_closed in prod/staging"))
	}
	// 摘要主密钥：生产必须配置，轮换期的历史密钥只用于双读。
	if isProdLikeEnv(c.Env) && strings.TrimSpace(c.Identity.HashKey) == "" {
		errs = append(errs, errors.New("identity.hash_key is required in prod/staging"))
	}
	if c.Identity.HashKey != "" || len(c.Identity.PreviousHashKeys) > 0 {
		active, keys, hashErr := c.Identity.DecodeHashKeys()
		if hashErr != nil {
			errs = append(errs, hashErr)
		} else {
			if !validHashKeyID(active) {
				errs = append(errs, fmt.Errorf("identity.hash_key_id is invalid: %q", active))
			}
			for _, item := range c.Identity.PreviousHashKeys {
				if !validHashKeyID(strings.TrimSpace(item.ID)) {
					errs = append(errs, fmt.Errorf("identity.previous_hash_keys id is invalid: %q", item.ID))
				}
			}
			for id := range keys {
				if !validHashKeyID(id) {
					errs = append(errs, fmt.Errorf("identity hash key id is invalid: %q", id))
				}
			}
		}
	}
	// MFA 仅 A 端实现（docs/identity-authn.md §7）：B / C 端既不校验也不支持绑定。
	switch c.Channel {
	case "admin":
		if isProdLikeEnv(c.Env) && !c.Identity.MFA.AdminRequired {
			errs = append(errs, errors.New("identity.mfa.admin_required must be true in prod/staging"))
		}
		if c.Identity.MFA.AdminRequired {
			keys, keysErr := c.Identity.MFA.DecodeKeys()
			if keysErr != nil {
				errs = append(errs, keysErr)
			}
			key, err := c.Identity.MFA.DecodeSecretKey()
			if err != nil {
				errs = append(errs, err)
			}
			if len(keys) == 0 && key == nil {
				errs = append(errs, errors.New("identity.mfa.secret_key or identity.mfa.keys is required when identity.mfa.admin_required=true"))
			}
		}
	case "biz":
		// 显式 fail fast：避免「配置写了强制 MFA，运行时却完全不校验」的假安全。
		if c.Identity.MFA.AdminRequired {
			errs = append(errs, errors.New("identity.mfa.admin_required must be false on channel=biz (MFA is admin-only)"))
		}
	}
	if strings.TrimSpace(c.Identity.MFA.Issuer) == "" {
		errs = append(errs, errors.New("identity.mfa.issuer must not be empty"))
	}
	if len(c.Identity.MFA.Keys) > 0 {
		keys, keysErr := c.Identity.MFA.DecodeKeys()
		if keysErr != nil {
			errs = append(errs, keysErr)
		}
		active := strings.TrimSpace(c.Identity.MFA.ActiveKeyID)
		switch active {
		case "":
			errs = append(errs, errors.New("identity.mfa.active_key_id is required when identity.mfa.keys is set"))
		default:
			if _, ok := keys[active]; !ok {
				errs = append(errs, fmt.Errorf("identity.mfa.active_key_id %q is not in identity.mfa.keys", active))
			}
		}
	}
	// PII subject 加密：配置密钥环时必须显式指定 active key，且密钥可解码。
	if len(c.Identity.Subject.Keys) > 0 {
		keys, keysErr := c.Identity.Subject.DecodeKeys()
		if keysErr != nil {
			errs = append(errs, keysErr)
		}
		active := strings.TrimSpace(c.Identity.Subject.ActiveKeyID)
		switch active {
		case "":
			errs = append(errs, errors.New("identity.subject.active_key_id is required when identity.subject.keys is set"))
		default:
			if _, ok := keys[active]; !ok {
				errs = append(errs, fmt.Errorf("identity.subject.active_key_id %q is not in identity.subject.keys", active))
			}
		}
		if _, legacyErr := c.Identity.Subject.DecodeLegacyKey(); legacyErr != nil {
			errs = append(errs, legacyErr)
		}
	}
	errs = append(errs, c.validateIdentitySMS()...)
	errs = append(errs, c.validateIdentityOAuth()...)
	return errs
}

// validateIdentityPassword 校验密码策略范围（IDN-P2-01）。
//
// 零值字段按默认策略（12～128）处理；错误只包含字段路径与边界值。
func (c *Config) validateIdentityPassword() []error {
	var errs []error
	minLength := c.Identity.Password.MinLength
	if minLength == 0 {
		minLength = identityDefaultPasswordMinLength
	}
	maxLength := c.Identity.Password.MaxLength
	if maxLength == 0 {
		maxLength = identityDefaultPasswordMaxLength
	}
	if minLength < identityMinPasswordLength {
		errs = append(errs, fmt.Errorf("identity.password.min_length must be >= %d", identityMinPasswordLength))
	}
	if maxLength < minLength {
		errs = append(errs, errors.New("identity.password.max_length must be >= identity.password.min_length"))
	}
	if maxLength > password.MaxPasswordBytes {
		errs = append(errs, fmt.Errorf("identity.password.max_length must be <= %d", password.MaxPasswordBytes))
	}
	if c.Identity.Password.MaxConcurrent < 0 {
		errs = append(errs, errors.New("identity.password.max_concurrent must be >= 0"))
	}
	// 0 表示由默认值填充（2s）。
	if c.Identity.Password.WaitTimeout != 0 {
		if c.Identity.Password.WaitTimeout < identityMinPasswordWaitTimeout {
			errs = append(errs, errors.New("identity.password.wait_timeout must be >= 100ms"))
		}
		if c.Identity.Password.WaitTimeout > identityMaxPasswordWaitTimeout {
			errs = append(errs, errors.New("identity.password.wait_timeout must be <= 30s"))
		}
	}
	if c.Identity.Password.HistoryCount < 0 || c.Identity.Password.HistoryCount > 20 {
		errs = append(errs, errors.New("identity.password.history_count must be in [0, 20]"))
	}
	if c.Identity.Password.HistoryWindow < 0 || c.Identity.Password.HistoryWindow > 5*365*24*time.Hour {
		errs = append(errs, errors.New("identity.password.history_window must be in [0, 5y]"))
	}
	return errs
}

// validateIdentitySMS 校验短信验证码参数（docs/identity-authn.md §8.1 / §10.2）。
func (c *Config) validateIdentitySMS() []error {
	sms := c.Identity.SMS
	var errs []error
	if sms.Enabled && c.Channel != "c" {
		errs = append(errs, errors.New("identity.sms.enabled is only supported on channel=c"))
	}
	// 空值等价于默认 memory（与 applyIdentityDefaults 保持一致）。
	driver := sms.Driver
	if driver == "" {
		driver = IdentitySMSDriverMemory
	}
	switch driver {
	case IdentitySMSDriverMemory, IdentitySMSDriverAliyun:
	default:
		errs = append(errs, fmt.Errorf("unsupported identity.sms.driver: %s", sms.Driver))
	}
	if !sms.Enabled {
		return errs
	}
	if strings.TrimSpace(sms.TemplateID) == "" {
		errs = append(errs, errors.New("identity.sms.template_id is required when identity.sms.enabled=true"))
	}
	if sms.CodeTTL <= 0 || sms.CodeTTL > 15*time.Minute {
		errs = append(errs, errors.New("identity.sms.code_ttl must be in (0, 15m]"))
	}
	if sms.MaxAttempts < 1 || sms.MaxAttempts > 10 {
		errs = append(errs, errors.New("identity.sms.max_attempts must be in [1, 10]"))
	}
	if sms.ResendInterval <= 0 || sms.ResendInterval > sms.CodeTTL {
		errs = append(errs, errors.New("identity.sms.resend_interval must be in (0, identity.sms.code_ttl]"))
	}
	if sms.DailyPerPhone < 1 {
		errs = append(errs, errors.New("identity.sms.daily_per_phone must be >= 1"))
	}
	if sms.HourlyPerIP < 1 {
		errs = append(errs, errors.New("identity.sms.hourly_per_ip must be >= 1"))
	}
	if sms.DailyBudget < 0 {
		errs = append(errs, errors.New("identity.sms.daily_budget must be >= 0"))
	}
	// 生产必须有全局硬预算，防止分布式号码池绕过单手机号 / IP 限额造成费用攻击。
	if isProdLikeEnv(c.Env) && sms.DailyBudget == 0 {
		errs = append(errs, errors.New("identity.sms.daily_budget must be > 0 in prod/staging"))
	}
	if sms.BreakerFailures < 0 {
		errs = append(errs, errors.New("identity.sms.breaker_failures must be >= 0"))
	}
	// 0 表示由默认值填充（60s）。
	if sms.BreakerTimeout != 0 {
		if sms.BreakerTimeout < identityMinSMSBreakerTimeout {
			errs = append(errs, errors.New("identity.sms.breaker_timeout must be >= 1s"))
		}
		if sms.BreakerTimeout > identityMaxSMSBreakerTimeout {
			errs = append(errs, errors.New("identity.sms.breaker_timeout must be <= 10m"))
		}
	}
	if isProdLikeEnv(c.Env) && driver == IdentitySMSDriverMemory {
		errs = append(errs, errors.New("identity.sms.driver must not be memory in prod/staging"))
	}
	return errs
}

// validateIdentityOAuth 校验 OAuth 提供方参数（docs/identity-authn.md §8.2 / §10.2）。
func (c *Config) validateIdentityOAuth() []error {
	providers := c.Identity.OAuth.Providers
	var errs []error
	if len(providers) > 0 && c.Channel != "c" {
		errs = append(errs, errors.New("identity.oauth.providers is only supported on channel=c"))
	}
	if c.Channel == "c" && isProdLikeEnv(c.Env) &&
		!c.Identity.SMS.Enabled && len(providers) == 0 {
		errs = append(errs, errors.New("channel=c requires identity.sms.enabled or at least one identity.oauth.providers in prod/staging"))
	}
	if len(providers) > 0 {
		rate := c.Identity.OAuth.RateLimit
		// 0 表示由默认值填充（1 rps / burst 5），直接构造 Config 的单测路径依赖该语义。
		if rate.RPS < 0 {
			errs = append(errs, errors.New("identity.oauth.rate_limit.rps must be >= 0"))
		}
		if rate.Burst < 0 {
			errs = append(errs, errors.New("identity.oauth.rate_limit.burst must be >= 0"))
		}
		switch rate.FailureMode {
		case "", RateLimitFailureClosed, RateLimitFailureOpen:
		default:
			errs = append(errs, fmt.Errorf("unsupported identity.oauth.rate_limit.failure_mode: %s", rate.FailureMode))
		}
		if isProdLikeEnv(c.Env) && rate.FailureMode == RateLimitFailureOpen {
			errs = append(errs, errors.New("identity.oauth.rate_limit.failure_mode must be fail_closed in prod/staging"))
		}
		// 0 表示默认值；显式配置时校验范围。
		if c.Identity.OAuth.Timeout != 0 &&
			(c.Identity.OAuth.Timeout < time.Second || c.Identity.OAuth.Timeout > 30*time.Second) {
			errs = append(errs, errors.New("identity.oauth.timeout must be in [1s, 30s]"))
		}
		if c.Identity.OAuth.BreakerFailures < 0 {
			errs = append(errs, errors.New("identity.oauth.breaker_failures must be >= 0"))
		}
		if c.Identity.OAuth.BreakerTimeout != 0 &&
			(c.Identity.OAuth.BreakerTimeout < time.Second || c.Identity.OAuth.BreakerTimeout > 10*time.Minute) {
			errs = append(errs, errors.New("identity.oauth.breaker_timeout must be in [1s, 10m]"))
		}
	}
	seen := make(map[string]struct{}, len(providers))
	for i, provider := range providers {
		path := fmt.Sprintf("identity.oauth.providers[%d]", i)
		name := strings.TrimSpace(provider.Name)
		if !isOAuthProviderName(name) {
			errs = append(errs, fmt.Errorf("%s.name must match ^[a-z][a-z0-9_-]{1,31}$", path))
		} else if _, dup := seen[name]; dup {
			errs = append(errs, fmt.Errorf("%s.name %q is duplicated", path, name))
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(provider.ClientID) == "" {
			errs = append(errs, fmt.Errorf("%s.client_id is required", path))
		}
		if strings.TrimSpace(provider.ClientSecretEnv) == "" {
			errs = append(errs, fmt.Errorf("%s.client_secret_env is required", path))
		}
		if strings.TrimSpace(provider.RedirectURI) == "" {
			errs = append(errs, fmt.Errorf("%s.redirect_uri is required", path))
		}
		for field, raw := range map[string]string{
			"auth_url":     provider.AuthURL,
			"token_url":    provider.TokenURL,
			"userinfo_url": provider.UserInfoURL,
			"redirect_uri": provider.RedirectURI,
		} {
			if err := validateAbsoluteURL(raw, isProdLikeEnv(c.Env)); err != nil {
				errs = append(errs, fmt.Errorf("%s.%s: %w", path, field, err))
			}
		}
	}
	return errs
}

// isOAuthProviderName 校验 provider 名：小写字母开头，允许 [a-z0-9_-]，长度 2-32。
func isOAuthProviderName(name string) bool {
	if len(name) < 2 || len(name) > 32 {
		return false
	}
	if name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		ch := name[i]
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

// validateAbsoluteURL 校验绝对 URL；requireHTTPS 为 true 时只接受 https。
func validateAbsoluteURL(raw string, requireHTTPS bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if requireHTTPS {
			return errors.New("must use https in prod/staging")
		}
		return nil
	default:
		return errors.New("scheme must be http or https")
	}
}

// validateAsync 校验异步运行时配置；未启用时只校验显式越界值。
func (c *Config) validateAsync() []error {
	var errs []error
	switch c.Async.Driver {
	case "", AsyncDriverDB:
	case AsyncDriverNATS:
		if c.Async.Enabled {
			errs = append(errs, c.validateAsyncBroker()...)
		}
	default:
		errs = append(errs, fmt.Errorf("async.driver %q is not implemented (only db and nats are supported)", c.Async.Driver))
	}
	if c.Async.Outbox.PollInterval != 0 &&
		(c.Async.Outbox.PollInterval < 100*time.Millisecond || c.Async.Outbox.PollInterval > time.Minute) {
		errs = append(errs, errors.New("async.outbox.poll_interval must be in [100ms, 1m]"))
	}
	if c.Async.Outbox.BatchSize != 0 && (c.Async.Outbox.BatchSize < 1 || c.Async.Outbox.BatchSize > 500) {
		errs = append(errs, errors.New("async.outbox.batch_size must be in [1, 500]"))
	}
	if c.Async.Outbox.Lease != 0 &&
		(c.Async.Outbox.Lease < 5*time.Second || c.Async.Outbox.Lease > 10*time.Minute) {
		errs = append(errs, errors.New("async.outbox.lease must be in [5s, 10m]"))
	}
	if c.Async.Outbox.MaxAttempts != 0 &&
		(c.Async.Outbox.MaxAttempts < 1 || c.Async.Outbox.MaxAttempts > 20) {
		errs = append(errs, errors.New("async.outbox.max_attempts must be in [1, 20]"))
	}
	if c.Async.Outbox.Concurrency != 0 &&
		(c.Async.Outbox.Concurrency < 1 || c.Async.Outbox.Concurrency > 16) {
		errs = append(errs, errors.New("async.outbox.concurrency must be in [1, 16]"))
	}
	if c.Async.Consumer.Concurrency != 0 &&
		(c.Async.Consumer.Concurrency < 1 || c.Async.Consumer.Concurrency > 32) {
		errs = append(errs, errors.New("async.consumer.concurrency must be in [1, 32]"))
	}
	if c.Async.Consumer.VisibilityTimeout != 0 &&
		(c.Async.Consumer.VisibilityTimeout < 5*time.Second ||
			c.Async.Consumer.VisibilityTimeout > 30*time.Minute) {
		errs = append(errs, errors.New("async.consumer.visibility_timeout must be in [5s, 30m]"))
	}
	switch c.Async.Consumer.RetryBackoff {
	case "", AsyncBackoffExponential, AsyncBackoffFixed:
	default:
		errs = append(errs, errors.New("async.consumer.retry_backoff must be exponential or fixed"))
	}
	if c.Async.Enabled && c.DB.Driver == "memory" {
		errs = append(errs, errors.New("async.enabled requires a persistent db.driver"))
	}
	if isProdLikeEnv(c.Env) && c.IAM.Mode == IAMModeEnforce && c.Async.Enabled && c.Async.Outbox.PublishInAPI {
		errs = append(errs, errors.New("async.outbox.publish_in_api must be false in prod/staging with iam.mode=enforce"))
	}
	return errs
}

// validateAsyncBroker 校验 NATS JetStream 连接与投递参数（async.driver=nats）。
func (c *Config) validateAsyncBroker() []error {
	var errs []error
	rawURL := strings.TrimSpace(c.Async.Broker.URL)
	if rawURL == "" {
		errs = append(errs, errors.New("async.broker.url is required when async.driver=nats"))
	} else {
		switch {
		case strings.HasPrefix(rawURL, "nats://"), strings.HasPrefix(rawURL, "tls://"),
			strings.HasPrefix(rawURL, "ws://"), strings.HasPrefix(rawURL, "wss://"):
		default:
			errs = append(errs, errors.New("async.broker.url must start with nats://, tls://, ws:// or wss://"))
		}
		if isProdLikeEnv(c.Env) &&
			!strings.HasPrefix(rawURL, "tls://") && !strings.HasPrefix(rawURL, "wss://") {
			errs = append(errs, errors.New("async.broker.url must use tls:// or wss:// in prod/staging"))
		}
	}
	if !validNATSStreamName(c.Async.Broker.Stream) {
		errs = append(errs, errors.New("async.broker.stream must be a non-empty ASCII alphanumeric/dash/underscore name"))
	}
	if !validNATSSubjectRoot(c.Async.Broker.SubjectPrefix) {
		errs = append(errs, errors.New("async.broker.subject_prefix must be a valid NATS subject prefix"))
	}
	if c.Async.Broker.AckWait != 0 &&
		(c.Async.Broker.AckWait < time.Second || c.Async.Broker.AckWait > 30*time.Minute) {
		errs = append(errs, errors.New("async.broker.ack_wait must be in [1s, 30m]"))
	}
	if c.Async.Broker.MaxDeliver != 0 &&
		(c.Async.Broker.MaxDeliver < 1 || c.Async.Broker.MaxDeliver > 100) {
		errs = append(errs, errors.New("async.broker.max_deliver must be in [1, 100]"))
	}
	return errs
}

// validNATSStreamName 校验 JetStream 流名称（NATS 不允许点号与通配符）。
func validNATSStreamName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// validNATSSubjectRoot 校验主题前缀：允许点号分层，但禁止通配符、空白与空层。
func validNATSSubjectRoot(prefix string) bool {
	if prefix == "" || strings.ContainsAny(prefix, " \t\r\n*>") {
		return false
	}
	return !strings.HasPrefix(prefix, ".") && !strings.HasSuffix(prefix, ".") &&
		!strings.Contains(prefix, "..")
}
