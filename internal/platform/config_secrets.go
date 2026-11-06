package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform/secrets"
)

// 密钥逻辑名（ENT-07 T4）。
//
// 命名规则 `<领域>_<用途>` 与 ADMIN_FRAME_* 环境变量后缀一致：
// EnvProvider（prefix=ADMIN_FRAME）可直接命中既有变量，迁移期无需改名。
//
//nolint:gosec // G101: 这些是密钥的逻辑名（文件/变量名），不含任何密钥值。
const (
	// SecretDBDSN 数据库连接串（含账号口令）。
	SecretDBDSN = "db_dsn"
	// SecretAuthJWTSecret 当前 HS256 签名密钥。
	SecretAuthJWTSecret = "auth_jwt_secret"
	// SecretAuthJWTSecrets 轮换期仍可校验的旧密钥，逗号分隔。
	SecretAuthJWTSecrets = "auth_jwt_secrets"
	// SecretIdentityHashKey 身份域带密钥摘要的密钥。
	SecretIdentityHashKey = "identity_hash_key"
	// SecretIdentityMFASecretKey TOTP 密钥的 AES-GCM 加密密钥。
	SecretIdentityMFASecretKey = "identity_mfa_secret_key"
	// SecretIdentitySAMLSPPrivateKey SAML SP 当前解密/签名私钥。
	SecretIdentitySAMLSPPrivateKey = "identity_saml_sp_private_key"
	// SecretIdentitySAMLSPPrivateKeyNext SAML SP 轮换私钥。
	SecretIdentitySAMLSPPrivateKeyNext = "identity_saml_sp_private_key_next"
	// SecretRedisPassword Redis 访问口令（哨兵 / 集群模式必填其一）。
	SecretRedisPassword = "redis_password"
	// SecretStorageS3AccessKey 对象存储 AK。
	SecretStorageS3AccessKey = "storage_s3_access_key"
	// SecretStorageS3SecretKey 对象存储 SK。
	SecretStorageS3SecretKey = "storage_s3_secret_key"
)

// 密钥来源枚举（secrets.providers 的合法取值）。
const (
	// SecretProviderEnv 从环境变量读取，变量名 <prefix>_<NAME 大写>。
	SecretProviderEnv = "env"
	// SecretProviderFile 从目录读取，文件名即密钥名，内容为密钥值。
	SecretProviderFile = "file"
	// SecretProviderVault 从 Vault KV 读取（HTTP API，无 SDK 依赖）。
	SecretProviderVault = "vault"
)

// secretResolveTimeout 限制启动期解析密钥的总耗时。
//
// env / file 为本地读取，正常在毫秒级完成；设置上限是为了在将来接入
// Vault / KMS 后，远端不可达时不会把进程卡死在启动阶段。
const secretResolveTimeout = 10 * time.Second

// SecretsConfig 是密钥托管配置（yaml: secrets）。
//
// 未配置 providers 时完全不启用 Provider，密钥仍由 ADMIN_FRAME_* 环境变量
// 经 BindEnv 注入 —— 保证既有部署升级后行为不变。显式配置后，Provider
// 命中的值**覆盖**配置文件与环境变量的值，使「密钥来自托管源」可被验证。
type SecretsConfig struct {
	// Providers 密钥来源顺序；按序尝试，首个命中的值生效。
	// 取值 env | file | vault；空表示不启用 Provider（回落到纯环境变量注入）。
	Providers []string `yaml:"providers"`
	// Env 环境变量来源参数。
	Env SecretsEnvConfig `yaml:"env"`
	// File 文件来源参数（K8s Secret / Vault Agent 挂载目录）。
	File SecretsFileConfig `yaml:"file"`
	// Vault Vault KV 来源参数（ENT-07 T3）。
	Vault SecretsVaultConfig `yaml:"vault"`
	// AllowEnvFallback 迁移期豁免：Provider 链尾自动追加 env 来源。
	//
	// 仅用于「已切换 file 但部分环境尚未挂载」的过渡期；prod 默认禁止
	// 仅 env 来源。迁移完成后置 false，缺失密钥即启动失败。
	AllowEnvFallback bool `yaml:"allow_env_fallback"`
}

// SecretsEnvConfig 是环境变量来源参数。
type SecretsEnvConfig struct {
	// Prefix 变量名前缀；默认 ADMIN_FRAME（与 config.envPrefix 一致）。
	Prefix string `yaml:"prefix"`
}

// SecretsFileConfig 是文件来源参数。
type SecretsFileConfig struct {
	// Dir 密钥文件目录；文件名即密钥逻辑名（如 db_dsn），内容为密钥值。
	Dir string `yaml:"dir"`
}

// SecretsVaultConfig 是 Vault KV 来源参数（ENT-07 T3）。
//
// 走 Vault HTTP API，不引入 Vault SDK：避免新增直接依赖与 DEP-06 评审面。
// 两种读数布局二选一：
//   - 单对象模式（path）：一个 KV 对象承载全部密钥，字段名即逻辑密钥名，启动期仅一次请求；
//   - 逐键模式（prefix）：每个密钥一个对象 `<prefix>/<name>`，取值读 value_field。
type SecretsVaultConfig struct {
	// Address Vault 地址；providers 含 vault 时必填。
	Address string `yaml:"address"`
	// Token 显式令牌；仅本地调试，prod 使用会被启动校验拒绝（SEC-05）。
	Token string `yaml:"token"`
	// TokenEnv 令牌所在环境变量名；默认 VAULT_TOKEN。
	TokenEnv string `yaml:"token_env"`
	// TokenFile 令牌文件路径（K8s 投影 / Vault Agent）。
	TokenFile string `yaml:"token_file"`
	// Mount KV 引擎挂载点；默认 secret。
	Mount string `yaml:"mount"`
	// Path 单对象模式的对象路径，如 admin-frame/prod。
	Path string `yaml:"path"`
	// Prefix 逐键模式的对象前缀，如 admin-frame/prod。
	Prefix string `yaml:"prefix"`
	// ValueField 逐键模式的取值字段名；默认 value。
	ValueField string `yaml:"value_field"`
	// Namespace Vault Enterprise 命名空间；空表示不设置。
	Namespace string `yaml:"namespace"`
	// CAFile 自签 Vault 的 CA 证书；空使用系统根证书。
	CAFile string `yaml:"ca_file"`
	// Timeout 单次请求超时；默认 5s。
	Timeout time.Duration `yaml:"timeout"`
	// CacheTTL 取值缓存时长（默认 60s）；负数禁用缓存。
	CacheTTL time.Duration `yaml:"cache_ttl"`
}

// LogValue 实现 slog.LogValuer：只输出来源结构，不输出任何密钥值（SEC-05）。
func (c SecretsConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("providers", c.Providers),
		slog.String("env_prefix", c.Env.Prefix),
		slog.String("file_dir", c.File.Dir),
		slog.Group("vault",
			slog.String("address", c.Vault.Address),
			slog.String("mount", c.Vault.Mount),
			slog.String("path", c.Vault.Path),
			slog.String("prefix", c.Vault.Prefix),
			slog.String("namespace", c.Vault.Namespace),
			slog.String("ca_file", c.Vault.CAFile),
			// 令牌只记录来源，不记录期望值，也不记录解析结果。
			slog.String("token_env", c.Vault.TokenEnv),
			slog.String("token_file", c.Vault.TokenFile),
			slog.Bool("inline_token", strings.TrimSpace(c.Vault.Token) != ""),
			slog.Duration("timeout", c.Vault.Timeout),
			slog.Duration("cache_ttl", c.Vault.CacheTTL),
		),
		slog.Bool("allow_env_fallback", c.AllowEnvFallback),
	)
}

// providerNames 返回去重后的来源顺序；allow_env_fallback 开启时在链尾补 env。
//
// 链尾追加而非前插：托管源必须优先，环境变量只作兜底。
func (c SecretsConfig) providerNames() []string {
	seen := make(map[string]bool, len(c.Providers)+1)
	names := make([]string, 0, len(c.Providers)+1)
	for _, raw := range c.Providers {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if c.AllowEnvFallback && !seen[SecretProviderEnv] {
		names = append(names, SecretProviderEnv)
	}
	return names
}

// effectiveEnvPrefix 返回生效的环境变量前缀。
func (c SecretsConfig) effectiveEnvPrefix() string {
	if prefix := strings.TrimSpace(c.Env.Prefix); prefix != "" {
		return prefix
	}
	return envPrefix
}

// buildSecretProvider 按来源顺序构造 Provider；无来源时返回 (nil, nil)。
//
// 返回 nil 表示调用方应保持既有注入路径不变，而不是「没有密钥」。
func buildSecretProvider(cfg SecretsConfig) (secrets.Provider, error) {
	names := cfg.providerNames()
	if len(names) == 0 {
		return nil, nil
	}
	providers := make([]secrets.Provider, 0, len(names))
	for _, name := range names {
		switch name {
		case SecretProviderEnv:
			providers = append(providers, secrets.NewEnvProvider(cfg.effectiveEnvPrefix()))
		case SecretProviderFile:
			providers = append(providers, secrets.NewFileProvider(cfg.File.Dir))
		case SecretProviderVault:
			// 构造期即校验地址/路径/令牌，失败直接阻断启动（依赖故障 fail fast）。
			provider, err := secrets.NewVaultProvider(secrets.VaultConfig{
				Address:    cfg.Vault.Address,
				Token:      cfg.Vault.Token,
				TokenEnv:   cfg.Vault.TokenEnv,
				TokenFile:  cfg.Vault.TokenFile,
				Mount:      cfg.Vault.Mount,
				Path:       cfg.Vault.Path,
				Prefix:     cfg.Vault.Prefix,
				ValueField: cfg.Vault.ValueField,
				Namespace:  cfg.Vault.Namespace,
				CAFile:     cfg.Vault.CAFile,
				Timeout:    cfg.Vault.Timeout,
				CacheTTL:   cfg.Vault.CacheTTL,
			})
			if err != nil {
				return nil, err
			}
			providers = append(providers, provider)
		default:
			return nil, fmt.Errorf("secrets.providers unsupported: %s", name)
		}
	}
	if len(providers) == 1 {
		return providers[0], nil
	}
	return secrets.NewChain(providers...)
}

// secretTarget 描述一个逻辑密钥最终写入的配置位置。
type secretTarget struct {
	name  string
	apply func(*Config, string)
}

// secretTargets 返回密钥逻辑名到配置字段的映射表。
//
// 新增密钥只需在此加一行；解析、覆盖与校验逻辑不需要改动。
func secretTargets() []secretTarget {
	return []secretTarget{
		{SecretDBDSN, func(c *Config, v string) { c.DB.DSN = v }},
		{SecretAuthJWTSecret, func(c *Config, v string) { c.Auth.JWT.Secret = v }},
		{SecretAuthJWTSecrets, func(c *Config, v string) { c.Auth.JWT.Secrets = splitSecretList(v) }},
		{SecretIdentityHashKey, func(c *Config, v string) { c.Identity.HashKey = v }},
		{SecretIdentityMFASecretKey, func(c *Config, v string) { c.Identity.MFA.SecretKey = v }},
		{SecretIdentitySAMLSPPrivateKey, func(c *Config, v string) { c.Identity.SAML.PrivateKey = v }},
		{SecretIdentitySAMLSPPrivateKeyNext, func(c *Config, v string) { c.Identity.SAML.PrivateKeyNextPEM = v }},
		{SecretRedisPassword, func(c *Config, v string) { c.Redis.Password = v }},
		{SecretStorageS3AccessKey, func(c *Config, v string) { c.Storage.S3.AccessKey = v }},
		{SecretStorageS3SecretKey, func(c *Config, v string) { c.Storage.S3.SecretKey = v }},
	}
}

// splitSecretList 把逗号分隔的密钥列表切成切片，丢弃空白项。
func splitSecretList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// applySecrets 用 Provider 解析密钥并回写配置。
//
// 语义：
//   - 未配置 providers → 直接返回，保持既有环境变量注入路径不变；
//   - 命中 → 覆盖配置文件 / 环境变量的值，使托管源成为唯一真相；
//   - 未命中（ErrNotFound）→ 保留原值，由既有字段校验决定是否致命，
//     避免在配置层重复实现一套「哪些字段必填」的判断。
//
// 错误只包含密钥逻辑名，不含取值（SEC-05）。
func (c *Config) applySecrets(ctx context.Context) error {
	provider, err := buildSecretProvider(c.Secrets)
	if err != nil {
		return err
	}
	if provider == nil {
		return nil
	}
	defer func() {
		// 关闭失败不影响启动：底层连接由进程退出回收。
		_ = provider.Close()
	}()

	for _, target := range secretTargets() {
		value, getErr := provider.Get(ctx, target.name)
		if getErr != nil {
			if errors.Is(getErr, secrets.ErrNotFound) {
				continue
			}
			return fmt.Errorf("secrets: read %s: %w", target.name, getErr)
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("secrets: %s resolved to empty value", target.name)
		}
		target.apply(c, value)
	}
	return nil
}

// resolveOne 用配置的来源解析单个密钥；返回空串表示该密钥未被托管。
//
// 供只加载部分配置段的工具（如 cmd/migrate）复用，避免它们绕过 Provider
// 直接读环境变量。
func (c SecretsConfig) resolveOne(name string) (string, error) {
	provider, err := buildSecretProvider(c)
	if err != nil {
		return "", err
	}
	if provider == nil {
		return "", nil
	}
	defer func() {
		_ = provider.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), secretResolveTimeout)
	defer cancel()
	value, getErr := provider.Get(ctx, name)
	if getErr != nil {
		if errors.Is(getErr, secrets.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("secrets: read %s: %w", name, getErr)
	}
	return strings.TrimSpace(value), nil
}

// resolveSecrets 是 normalize 阶段的入口：带超时执行解析并暂存错误。
//
// Loader 的 Normalize 钩子不返回错误，故把结果暂存到 Config.secretErr，
// 由 Validate 汇总进启动错误；这样密钥缺失仍然能在启动期阻断进程。
func (c *Config) resolveSecrets() {
	ctx, cancel := context.WithTimeout(context.Background(), secretResolveTimeout)
	defer cancel()
	c.secretErr = c.applySecrets(ctx)
}

// validateSecrets 校验密钥来源配置的合法性与生产约束（ENT-07）。
//
// 规则：
//   - providers 只允许 env / file / vault；
//   - file 来源必须给出目录；
//   - vault 来源在 prod 下禁止显式令牌（必须由 token_file / token_env 注入）；
//   - prod 下不得仅以 env 作为来源，除非显式开启 allow_env_fallback（迁移期）。
//
// vault 的结构性校验（地址、path/prefix 二选一、令牌是否存在）由
// secrets.NewVaultProvider 在构造期完成，错误经 normalize 阶段汇入 secretErr；
// 此处只承担「运行环境相关的策略判断」，避免同一问题报两遍。
//
// 不强制 prod 必须声明 providers：未声明时保持「环境变量注入」的既有行为，
// 避免升级即导致存量部署启动失败。生产制品必须托管由 `make check-secret-provider`
// 在 deploy 层强制（Helm values、prod 样例配置），运行时不重复承担该判断。
func (c *Config) validateSecrets() []error {
	var errs []error
	prod := isProdEnv(c.Env)

	for _, name := range c.Secrets.providerNames() {
		switch name {
		case SecretProviderEnv:
			if prod && !c.Secrets.AllowEnvFallback {
				errs = append(errs, errors.New(
					"secrets.providers must not include env when env=prod; set allow_env_fallback only for migration (ENT-07)"))
			}
		case SecretProviderFile:
			if strings.TrimSpace(c.Secrets.File.Dir) == "" {
				errs = append(errs, errors.New("secrets.file.dir is required when secrets.providers includes file"))
			}
		case SecretProviderVault:
			// 令牌明文出现在配置里等于没有托管：prod 只允许文件 / 环境变量注入。
			if prod && strings.TrimSpace(c.Secrets.Vault.Token) != "" {
				errs = append(errs, errors.New(
					"secrets.vault.token must be empty when env=prod; use token_file or token_env (SEC-05)"))
			}
		default:
			errs = append(errs, fmt.Errorf("secrets.providers unsupported: %s", name))
		}
	}
	return errs
}
