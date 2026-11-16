// Package appconfig 组合加载平台段与业务域配置。
//
// 平台根 internal/platform/config 只负责进程级基础设施配置（HTTP、DB、日志、
// 异步运行时等）；Identity / IAM / Audit / Tenant 的配置结构与默认值、校验
// 规则分别由各域包自持。本包是唯一把各段拼在一起的装配点，避免平台包反向
// 依赖业务域，也让各 cmd 显式选择需要加载的域配置。
package appconfig

import (
	"errors"
	"fmt"
	"strings"

	auditconfig "github.com/admin-frame/admin-frame/internal/audit/config"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
	iamcatalog "github.com/admin-frame/admin-frame/internal/iam/catalog"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	identityconfig "github.com/admin-frame/admin-frame/internal/identity/config"
	notificationconfig "github.com/admin-frame/admin-frame/internal/notification/config"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	tenantconfig "github.com/admin-frame/admin-frame/internal/tenant/config"
	"github.com/admin-frame/admin-frame/pkg/config"
)

// Config 是进程完整配置：平台段内联展开，域配置按 yaml 顶层段各自挂载。
type Config struct {
	platformconfig.Config `yaml:",inline"`

	Identity     identityconfig.IdentityConfig         `yaml:"identity"`
	IAM          iamconfig.IAMConfig                   `yaml:"iam"`
	Audit        auditconfig.AuditConfig               `yaml:"audit"`
	Tenant       tenantconfig.TenantConfig             `yaml:"tenant"`
	Notification notificationconfig.NotificationConfig `yaml:"notification"`
}

// ApplyDefaults 依次填充平台段与各域默认值。
func (c *Config) ApplyDefaults() {
	c.Config.ApplyDefaults()
	c.Identity.ApplyDefaults()
	c.IAM.ApplyDefaults()
	c.Audit.ApplyDefaults()
	c.Notification.ApplyDefaults()
}

// Normalize 补齐平台段派生值、归一化 IAM 模式，并在同一次 Provider 解析中
// 回写平台与身份域密钥。
func (c *Config) Normalize() error {
	if err := c.NormalizeCore(); err != nil {
		return err
	}
	c.IAM.Normalize()
	return c.ResolveSecretsWithTargets(c.domainSecretTargets())
}

// domainSecretTargets 返回域配置的密钥回写目标。
//
// 逻辑名常量仍定义在平台 config 包（密钥命名属于平台约定），域包不感知
// secret provider 的存在。
func (c *Config) domainSecretTargets() []platformconfig.SecretTarget {
	return []platformconfig.SecretTarget{
		{Name: platformconfig.SecretIdentityHashKey, Apply: func(v string) { c.Identity.HashKey = v }},
		{Name: platformconfig.SecretIdentityMFASecretKey, Apply: func(v string) { c.Identity.MFA.SecretKey = v }},
		{Name: platformconfig.SecretIdentitySAMLSPPrivateKey, Apply: func(v string) { c.Identity.SAML.PrivateKey = v }},
		{Name: platformconfig.SecretIdentitySAMLSPPrivateKeyNext, Apply: func(v string) { c.Identity.SAML.PrivateKeyNextPEM = v }},
	}
}

// Validate 聚合平台段与全部域配置的错误，保持启动期一次性列出所有问题。
func (c *Config) Validate() error {
	errs := c.ValidateErrors(c.IAM.Mode)
	errs = append(errs,
		c.IAM.Validate(c.Env, c.Channel, c.DB.Driver, c.Redis.Enabled(), c.Async.Enabled),
		c.Identity.Validate(c.Env, c.Channel, c.Auth.EffectiveMode(c.Env), c.DB.Driver, c.Redis.Enabled(), c.Async.Enabled),
		c.Audit.Validate(c.Env, c.DB.Driver, c.Storage.Driver),
		c.Tenant.Validate(),
		c.validateM2MScopes(),
	)
	return errors.Join(errs...)
}

// validateM2MScopes 校验 identity.m2m.allowed_scopes 均为 IAM 活跃权限码（§21.2.6）。
//
// 身份域不 import IAM，权限目录校验只能落在装配层：未知权限码启动失败，
// 避免「配置写了 scope，运行期却永远无法授权」的假启用。
func (c *Config) validateM2MScopes() error {
	if !c.Identity.M2M.Enabled {
		return nil
	}
	known := make(map[string]struct{}, len(iamcatalog.Codes()))
	for _, code := range iamcatalog.Codes() {
		known[string(code)] = struct{}{}
	}
	var errs []error
	for _, scope := range c.Identity.M2M.AllowedScopes {
		code := strings.TrimSpace(scope)
		if _, ok := known[code]; !ok {
			errs = append(errs, fmt.Errorf(
				"identity.m2m.allowed_scopes contains unknown permission code: %s", scope))
		}
	}
	return errors.Join(errs...)
}

// NewLoader 构造完整的进程配置加载器。
func NewLoader(path string) (*config.Loader[Config], error) {
	return NewLoaderWithValues(path, nil)
}

// NewLoaderWithValues 构造带配置中心快照覆盖的加载器。
//
// values 返回已验签快照的扁平取值（yaml 路径 → 字符串）；nil 表示不接入。
// 环境变量仅放行 db/redis（InfraEnvPathAllowlist）。
func NewLoaderWithValues(path string, values func() (map[string]string, error)) (*config.Loader[Config], error) {
	return config.NewLoader(config.Options[Config]{
		Files:            []string{path},
		EnvPrefix:        platformconfig.EnvPrefix,
		EnvPathAllowlist: platformconfig.InfraEnvPathAllowlist,
		Values:           values,
		ValueAliases:     spec.ValueAliases(),
		Defaults:         (*Config).ApplyDefaults,
		Normalize:        (*Config).Normalize,
		Validate:         (*Config).Validate,
	})
}

// LoadWithValuesAllowMissing 在允许缺失配置文件时加载（无文件启动）。
//
// allowMissing 只控制 yaml 文件是否可缺；业务取值必须由调用方提供的签名
// 快照 Values 覆盖，不得把「无快照」误读为允许仅用代码默认值启动。
// 环境变量仅放行 db/redis。
func LoadWithValuesAllowMissing(path string, values func() (map[string]string, error),
	allowMissing bool) (*Config, error) {
	loader, err := config.NewLoader(config.Options[Config]{
		Files:             []string{path},
		EnvPrefix:         platformconfig.EnvPrefix,
		EnvPathAllowlist:  platformconfig.InfraEnvPathAllowlist,
		Values:            values,
		ValueAliases:      spec.ValueAliases(),
		Defaults:          (*Config).ApplyDefaults,
		Normalize:         (*Config).Normalize,
		Validate:          (*Config).Validate,
		AllowMissingFiles: allowMissing,
	})
	if err != nil {
		return nil, err
	}
	return loader.Load()
}

// Load 从可选 YAML 文件加载完整配置，应用默认值与白名单环境变量后校验。
//
// CFG-04：业务取值以「配置中心快照 > 代码默认值」为准；无 Values 时仅文件与
// db/redis 环境变量参与，不得把 ADMIN_FRAME_* 业务键当作事实源。
func Load(path string) (*Config, error) {
	return LoadWithValues(path, nil)
}

// LoadWithValues 加载配置并应用配置中心快照取值。
//
// 优先级：配置中心快照 > 可选文件 > 代码默认值；ADMIN_FRAME_DB_*/REDIS_*
// 作为基础设施补充通道在快照之后覆盖（CFG-04）。
func LoadWithValues(path string, values func() (map[string]string, error)) (*Config, error) {
	loader, err := NewLoaderWithValues(path, values)
	if err != nil {
		return nil, err
	}
	return loader.Load()
}
