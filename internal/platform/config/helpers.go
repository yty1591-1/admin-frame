package config

import (
	"strings"

	"github.com/admin-frame/admin-frame/internal/shared/deployenv"
	"github.com/admin-frame/admin-frame/pkg/config"
)

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

// IsProdEnv 报告是否为生产环境，供平台装配层复用同一判定。
func IsProdEnv(env string) bool { return deployenv.IsProd(env) }

// IsProdLikeEnv 报告环境是否按生产标准校验（prod / staging）。
func IsProdLikeEnv(env string) bool { return deployenv.IsProdLike(env) }

// IsLocalDevEnv 报告是否为允许本地开发回退的环境（local / dev / test）。
//
// staging/prod 以及未声明（空）一律返回 false，避免「Validate 后补 Env」或
// 漏注入时误开源码内固定密钥（CC-R-02）。
func IsLocalDevEnv(env string) bool { return deployenv.IsLocalDev(env) }

// 内部别名：历史调用点仍用小写名。
func isProdEnv(env string) bool     { return deployenv.IsProd(env) }
func isProdLikeEnv(env string) bool { return deployenv.IsProdLike(env) }

// NewConfigLoader 构造进程配置加载器。
//
// 当前所有进程只在启动期加载一次（LoadConfig）；pkg/config.Watcher 的热更新
// 能力为预留项，尚未接入任何 cmd。启用前须先明确可热更新字段白名单并写 ADR
// （CONFIG_STANDARDS §9）。保持导出是为了让未来的长驻进程复用同一套校验。
func NewConfigLoader(path string) (*config.Loader[Config], error) {
	return config.NewLoader(config.Options[Config]{
		Files:            []string{path},
		EnvPrefix:        envPrefix,
		EnvPathAllowlist: InfraEnvPathAllowlist,
		Defaults:         (*Config).applyDefaults,
		Normalize:        (*Config).normalize,
		Validate:         (*Config).Validate,
	})
}

// LoadConfig 从 YAML 文件加载配置，应用默认值与白名单环境变量后校验。
//
// 优先级：配置中心快照（若接入）> ADMIN_FRAME_DB_* > 文件 > 默认值。
// Redis 口令等已迁出 env 白名单，改由密钥子域注入。
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
	DB      DBConfig      `yaml:"db"`
	Secrets SecretsConfig `yaml:"secrets"`
}

// LoadDBConfig 仅加载配置文件的 db 段，并应用 ADMIN_FRAME_DB_* 环境变量覆盖。
//
// 供 cmd/migrate 等只需要数据库连接的工具复用 CFG-04 的配置优先级；
// 不校验服务级字段（service/channel/addr）。path 为空时只读取环境变量。
// 传入完整服务配置时其余字段被忽略（AllowUnknownFields），便于直接复用线上 yaml。
func LoadDBConfig(path string) (DBConfig, error) {
	opts := config.Options[dbOnlyConfig]{
		EnvPrefix:          envPrefix,
		EnvPathAllowlist:   []string{"db"},
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
	// 与主配置同源：secrets.providers 命中时覆盖 DSN，未配置时保持环境变量注入。
	// 迁移工具（cmd/migrate）同样不能绕过托管源，否则会出现「服务端走 Vault、
	// 迁移仍读环境变量」的分裂。
	if value, resolveErr := cfg.Secrets.resolveOne(SecretDBDSN); resolveErr != nil {
		return DBConfig{}, resolveErr
	} else if value != "" {
		cfg.DB.DSN = value
	}
	return cfg.DB, nil
}
