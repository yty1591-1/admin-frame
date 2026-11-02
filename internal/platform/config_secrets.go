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
	// 取值 env | file；空表示不启用 Provider（回落到纯环境变量注入）。
	Providers []string `yaml:"providers"`
	// Env 环境变量来源参数。
	Env SecretsEnvConfig `yaml:"env"`
	// File 文件来源参数（K8s Secret / Vault Agent 挂载目录）。
	File SecretsFileConfig `yaml:"file"`
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

// LogValue 实现 slog.LogValuer：只输出来源结构，不输出任何密钥值（SEC-05）。
func (c SecretsConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("providers", c.Providers),
		slog.String("env_prefix", c.Env.Prefix),
		slog.String("file_dir", c.File.Dir),
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
//   - providers 只允许 env / file；
//   - file 来源必须给出目录；
//   - prod 下不得仅以 env 作为来源，除非显式开启 allow_env_fallback（迁移期）。
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
		default:
			errs = append(errs, fmt.Errorf("secrets.providers unsupported: %s", name))
		}
	}
	return errs
}
