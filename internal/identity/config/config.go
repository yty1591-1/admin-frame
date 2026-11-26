package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	// SAML C 端 SAML 2.0 SP-initiated SSO 参数。
	SAML IdentitySAMLConfig `yaml:"saml"`
	// M2M 服务间机器身份（client_credentials）参数（§21.2）；默认关闭。
	M2M IdentityM2MConfig `yaml:"m2m"`
	// SCIM 2.0 目录同步参数（§21.1）；仅 A 端，默认关闭。
	SCIM IdentitySCIMConfig `yaml:"scim"`
	// Consumer C 端账号自助操作参数。
	Consumer IdentityConsumerConfig `yaml:"consumer"`
	// Subject 账号登录主体的 PII 加密 / 索引参数（IDN-GAP-P1-06）。
	//
	// 必填：最终 schema 不含明文主体列，subject_index 与 subject_ciphertext 均依赖本配置；
	// 未配置时仓储无法查找或写入账号（失败关闭）。
	Subject IdentitySubjectConfig `yaml:"subject"`
}

// IdentityConsumerConfig 是 C 端账号自助操作参数（8.4 R-8.4-04）。
type IdentityConsumerConfig struct {
	// DeleteReauthWindow 自助注销要求的最近登录窗口；0 使用默认 10m，范围 [1m, 30m]。
	//
	// OAuth-only 账号没有手机号可收验证码，仅凭 Bearer 注销风险过高；
	// 窗口由设备会话的创建时间（真实登录时刻）判定，超出即要求重新登录后再注销。
	DeleteReauthWindow time.Duration `yaml:"delete_reauth_window"`
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
	// DefaultRegion 可选默认地区（ISO 3166-1 alpha-2，如 CN）；配置后允许本地号码
	// 自动补国家码，未配置时仅接受 E.164（BIZ-07）。
	DefaultRegion string `yaml:"default_region"`
	// QuotaTimezone 短信日配额 / 全局预算的时区（IANA 名称）；空表示 UTC（BIZ-05）。
	QuotaTimezone string `yaml:"quota_timezone"`
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
	// OIDCEnabled 显式启用 OIDC（id_token 验签）；默认关闭。
	//
	// 关闭时即使误填 issuer / jwks_url 也不会启用 OIDC，且启动校验直接失败关闭。
	OIDCEnabled bool `yaml:"oidc_enabled"`
	// Issuer 与 JWKSURL 在 oidc_enabled=true 时必填：以 id_token 的签名与 issuer 为准。
	Issuer string `yaml:"issuer"`
	// JWKSURL OIDC 公钥集端点；随 Issuer 成对出现。
	JWKSURL string `yaml:"jwks_url"`
	// Audience id_token 期望的 audience；空时使用 ClientID。
	Audience string `yaml:"audience"`
	// JWKSTTL 公钥缓存时长；0 使用 15m，范围 [1m, 24h]。
	JWKSTTL time.Duration `yaml:"jwks_ttl"`
	// JWKSMinRefreshInterval 未知 kid 触发强制刷新的最小间隔；0 使用 1m。
	JWKSMinRefreshInterval time.Duration `yaml:"jwks_min_refresh_interval"`
	// IDTokenLeeway id_token 时间字段时钟偏差容忍；0 使用 30s，范围 [0, 5m]。
	IDTokenLeeway time.Duration `yaml:"id_token_leeway"`
}

// IdentitySAMLConfig 是 C 端 SAML 2.0 SP-initiated SSO 参数（docs/identity-authn.md §8.3 / §10）。
type IdentitySAMLConfig struct {
	// Enabled 是否启用 SAML 登录；关闭时不注册任何 SAML 路由。
	Enabled bool `yaml:"enabled"`
	// EntityID SP 实体标识，写入 AuthnRequest 与元数据。
	EntityID string `yaml:"entity_id"`
	// ACSURL IdP 回传断言的绝对地址，路径必须为 /c/v1/auth/saml/acs。
	ACSURL string `yaml:"acs_url"`
	// LoginRedirectURL ACS 成功后的前端登录页绝对地址；禁止用户输入参与拼接。
	LoginRedirectURL string `yaml:"login_redirect_url"`
	// CertificatePEM SP 签名 / 解密证书（可公开）。
	CertificatePEM string `yaml:"certificate_pem"`
	// PrivateKey PEM 私钥；生产由密钥托管注入，禁止写入明文 YAML。
	PrivateKey string `yaml:"private_key"`
	// CertificateNextPEM / PrivateKeyNextPEM 轮换用下一把密钥；必须成对出现。
	CertificateNextPEM string `yaml:"certificate_next_pem"`
	PrivateKeyNextPEM  string `yaml:"private_key_next"`
	// AutoProvision 未映射主体是否自动建号；默认关闭（失败关闭）。
	AutoProvision bool `yaml:"auto_provision"`
	// NameIDFormat AuthnRequest 请求的 NameID 格式：persistent（默认）/ transient / unspecified。
	// 不使用 transient：每次登录主体都会变化，auto_provision 会重复建号。
	NameIDFormat string `yaml:"name_id_format"`
	// Timeout 单次 IdP 元数据拉取超时；0 表示默认 5s。
	Timeout time.Duration `yaml:"timeout"`
	// MetadataTTL IdP 元数据缓存时长；0 表示默认 15m。
	MetadataTTL time.Duration `yaml:"metadata_ttl"`
	// MetadataMinRefresh 未知签名证书触发强刷的最小间隔；0 表示默认 1m。
	MetadataMinRefresh time.Duration `yaml:"metadata_min_refresh"`
	// ClockSkew 断言时间条件允许的时钟偏差；0 表示默认 2m，上限 5m。
	ClockSkew time.Duration `yaml:"clock_skew"`
	// AssertionTTL 单条断言最大生命周期；0 表示默认 5m，上限 15m。
	AssertionTTL time.Duration `yaml:"assertion_ttl"`
	// RequestTTL AuthnRequest / state 有效期；0 表示默认 10m，上限 15m。
	RequestTTL time.Duration `yaml:"request_ttl"`
	// ReplayTTL 断言 ID 去重窗口；必须覆盖 AssertionTTL + ClockSkew。
	ReplayTTL time.Duration `yaml:"replay_ttl"`
	// TicketTTL 一次性兑换票据有效期；0 表示默认 60s，范围 [30s, 5m]。
	TicketTTL time.Duration `yaml:"ticket_ttl"`
	// RateLimit authorize / acs 的限流参数（与 OAuth 同语义）。
	RateLimit IdentityLoginRateLimitConfig `yaml:"rate_limit"`
	// Providers 已配置的 IdP；为空且 enabled=true 时启动失败。
	Providers []IdentitySAMLProviderConfig `yaml:"providers"`
}

// IdentitySAMLProviderConfig 是单个 SAML IdP 参数。
//
// metadata_url 与 metadata_xml 二选一，二者同时提供视为配置错误。
type IdentitySAMLProviderConfig struct {
	// Name 提供方名，写入路由与映射表；小写字母开头，允许 [a-z0-9_-]。
	Name string `yaml:"name"`
	// IDPEntityID IdP 实体标识；metadata 中的 entityID 必须与之一致。
	IDPEntityID string `yaml:"idp_entity_id"`
	// MetadataURL IdP 元数据地址；与 MetadataXML 二选一。
	MetadataURL string `yaml:"metadata_url"`
	// MetadataXML 内联 IdP 元数据；与 MetadataURL 二选一。
	MetadataXML string `yaml:"metadata_xml"`
}

// IdentityM2MConfig 是 M2M 机器身份参数（docs/identity-authn.md §21.2 / §10.1）。
//
// 默认关闭：enabled=false 时三个 /{端}/v1/auth/token 路由均不注册（404），
// 不读表、不参与健康检查；开启但配置非法必须启动失败。
type IdentityM2MConfig struct {
	// Enabled 是否启用 client_credentials 令牌端点。
	Enabled bool `yaml:"enabled"`
	// TokenTTL 机器令牌有效期；范围 [5m, 1h]，默认 15m，调用方不可指定。
	TokenTTL time.Duration `yaml:"token_ttl"`
	// RotateGrace 旧 secret 的退役宽限；范围 [0, 168h]，默认 24h。
	RotateGrace time.Duration `yaml:"rotate_grace"`
	// MaxSecretAge secret 最长生命周期；范围 [720h, 8760h]，默认 2160h（90d）。
	//
	// 超龄只标记 rotation_due 并告警，不自动失效，避免生产断链。
	MaxSecretAge time.Duration `yaml:"max_secret_age"`
	// AllowedScopes 允许申请的最大权限码集合；启用时必填，禁止通配符。
	AllowedScopes []string `yaml:"allowed_scopes"`
	// RateLimit 签发端点限流（按 client_id，叠加单 IP）；Redis fail_closed。
	RateLimit IdentityLoginRateLimitConfig `yaml:"rate_limit"`
}

// ScopeAllowed 报告权限码是否在白名单内（精确匹配，不支持通配）。
func (m IdentityM2MConfig) ScopeAllowed(scope string) bool {
	target := strings.TrimSpace(scope)
	if target == "" {
		return false
	}
	for _, item := range m.AllowedScopes {
		if item == target {
			return true
		}
	}
	return false
}

// IdentitySCIMConfig 是 SCIM 2.0 目录同步参数（docs/identity-authn.md §21.1 / §10.1）。
//
// 默认关闭：enabled=false 时不注册 /admin/scim/v2 路由，不读 SCIM 表、不参与健康检查；
// 仅 admin-api 允许开启，biz-api / c-api 出现 enabled=true 直接启动失败。
type IdentitySCIMConfig struct {
	// Enabled 是否启用 SCIM 2.0 服务端。
	Enabled bool `yaml:"enabled"`
	// Provider 目录提供方标识；启用时必填，写入映射表为 scim:<provider>。
	Provider string `yaml:"provider"`
	// AutoProvisionPtr 未绑定账号时是否按 User 直接建号；nil（未配置）视为 true。
	//
	// 用指针区分「未配置」与「显式 false」：默认 true 是 IdP 推送即建的预期语义，
	// 显式 false 才表示账号必须先经 SSO 首登建立，SCIM 只做更新。
	AutoProvisionPtr *bool `yaml:"auto_provision"`
	// GroupRoleMap SCIM displayName → A 端角色 code；未映射的组只同步成员关系。
	GroupRoleMap map[string]string `yaml:"group_role_map"`
	// PageSize 列表默认每页条数；范围 [1, 100]，默认 50。
	PageSize int `yaml:"page_size"`
	// MaxPayload 单请求体上限（字节）；范围 [64KiB, 2MiB]，默认 1MiB。
	MaxPayload int64 `yaml:"max_payload"`
	// RequestTimeout 单请求处理超时；范围 [1s, 30s]，默认 10s。
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// TokenGrace 旧 SCIM 令牌退役宽限；范围 [0, 720h]，默认 168h。
	TokenGrace time.Duration `yaml:"token_grace"`
	// RateLimit SCIM 端点限流（单令牌，叠加单 IP）；Redis fail_closed。
	RateLimit IdentityLoginRateLimitConfig `yaml:"rate_limit"`
}

// MappedRole 返回组 displayName 对应的角色 code；未映射返回空串。
func (s IdentitySCIMConfig) MappedRole(displayName string) string {
	if len(s.GroupRoleMap) == 0 {
		return ""
	}
	return strings.TrimSpace(s.GroupRoleMap[strings.TrimSpace(displayName)])
}

// AutoProvision 报告是否允许 SCIM 直接建号；未配置时为 true（§21.1.7）。
func (s IdentitySCIMConfig) AutoProvision() bool {
	return s.AutoProvisionPtr == nil || *s.AutoProvisionPtr
}

// IdentityMFAConfig 是 A 端多因素认证参数（docs/identity-authn.md §10）。
type IdentityMFAConfig struct {
	// AdminRequired 表示 A 端密码登录后必须完成 TOTP 才能获得管理令牌；生产强制开启。
	AdminRequired bool `yaml:"admin_required"`
	// Channels 允许启用 MFA 的端别；空表示仅 admin（BIZ-03）。只支持 admin / biz。
	Channels []string `yaml:"channels"`
	// BizRequired 表示 B 端强制绑定 MFA；需同时把 biz 加入 Channels。
	BizRequired bool `yaml:"biz_required"`
	// Factors 是允许启用的多因素因子白名单；空表示仅 totp（BIZ-03）。
	// 当前只实现 totp；新增 WebAuthn / 推送时必须先在本字段与运行期同步支持，
	// 未实现的因子在启动期直接失败，避免「配置声明但运行不生效」的假安全。
	Factors []string `yaml:"factors"`
	// Issuer 写入 otpauth URI，默认 AdminFrame。
	Issuer string `yaml:"issuer"`
	// SecretKey 是 TOTP 密钥的 AES-256 主密钥，仅允许通过环境变量注入（SEC-01）。
	// 未配置 keys 时作为唯一密钥；配置 keys 后作为旧密文的解密回退。
	SecretKey string `yaml:"secret_key"`
	// Keys 是 MFA 密钥环，支持按 key id 轮换（IDN-P1-05）。
	Keys []IdentityMFAKeyConfig `yaml:"keys"`
	// ActiveKeyID 指定当前加密密钥；Keys 非空时必填。
	ActiveKeyID string `yaml:"active_key_id"`
	// TOTPDigits 动态码位数；0 表示默认 6，仅支持 6 / 8（MNT-05）。
	TOTPDigits int `yaml:"totp_digits"`
	// TOTPStep 时间步长；0 表示默认 30s，范围 [15s, 120s]。
	TOTPStep time.Duration `yaml:"totp_step"`
	// TOTPWindow 允许的窗口偏移；0 表示默认 1，范围 [0, 2]。
	TOTPWindow int `yaml:"totp_window"`
	// RecoveryCodeCount 恢复码数量；0 表示默认 10，范围 [5, 20]。
	RecoveryCodeCount int `yaml:"recovery_code_count"`
	// StepUpReuseGrace 登录后立即 step-up 复用已消费时间步的宽限；0 表示默认 2m，
	// 负值显式关闭，上限 10m（BIZ-08）。
	StepUpReuseGrace time.Duration `yaml:"step_up_reuse_grace"`
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
	// TemporaryLength 管理员重置口令的临时密码长度；0 表示默认 20，范围 [16, 32]（MNT-05）。
	TemporaryLength int `yaml:"temporary_length"`
	// TenantOverrides 商户级密码策略覆盖（BIZ-04）；只能收紧、不能弱于全局下限。
	TenantOverrides []IdentityPasswordTenantOverride `yaml:"tenant_overrides"`
}

// IdentityPasswordTenantOverride 是单个商户的密码策略覆盖。
//
// 安全下限：min_length 只能 >= 全局 min_length；max_length 只能 <= 全局 max_length；
// require_complexity 只能为 true（不得关闭全局要求）。
type IdentityPasswordTenantOverride struct {
	// MerchantID 商户 ID；按规范化小写匹配。
	MerchantID string `yaml:"merchant_id"`
	// MinLength 最小长度；0 表示沿用全局。
	MinLength int `yaml:"min_length"`
	// MaxLength 最大长度；0 表示沿用全局。
	MaxLength int `yaml:"max_length"`
	// RequireComplexity 是否要求三类字符；nil 表示沿用全局。
	RequireComplexity *bool `yaml:"require_complexity"`
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
	IPRPS   float64 `yaml:"ip_rps"`
	IPBurst int     `yaml:"ip_burst"`
	// FailureMode Redis 故障策略：fail_closed（默认）| fail_open；prod/staging 强制 fail_closed。
	FailureMode string `yaml:"failure_mode"`
}

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
	// TokenGrace jti 本地宽限；0 使用默认 30s，负数显式关闭，上限 60s（8.4 R-8.4-01）。
	//
	// Redis 故障时允许最近一次已验证未撤销的 jti 在该窗口内继续使用，
	// 避免 fail_closed 放大为全量 503；代价是撤销可见延迟最多一个窗口。
	TokenGrace time.Duration `yaml:"token_grace"`
}
