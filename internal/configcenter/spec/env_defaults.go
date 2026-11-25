package spec

import (
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// 默认值来源（与 OpenAPI SchemaDefault.source / 前端徽标对齐）。
const (
	// DefaultSourceNone 表示无推荐默认值（敏感键、或声明与叠加层皆空）。
	DefaultSourceNone = "none"
	// DefaultSourceCode 表示取自 KeySpec.Default（代码声明，通常本地友好）。
	DefaultSourceCode = "code_default"
	// DefaultSourceProd 表示取自 staging/prod 环境推荐叠加层。
	DefaultSourceProd = "prod_default"
	// DefaultSourceDev 表示取自 local/dev/test 环境推荐叠加层。
	DefaultSourceDev = "dev_default"
)

// ResolveDefault 按环境解析「推荐默认值」与来源。
//
// 优先级（CONFIG_STANDARDS §4.1 / CFG-02）：
//
//	环境叠加层（本文件） > KeySpec.Default > 空
//
// 安装向导 schema、Bootstrap 播种、reset-defaults 自愈必须走同一解析，
// 避免「向导提示的默认值」与「实际播种值」分叉。敏感键永不返回明文默认值。
func ResolveDefault(env string, keySpec domain.KeySpec) (value, source string) {
	if keySpec.Sensitive {
		return "", DefaultSourceNone
	}
	if overlay, overlaySource := envOverlay(env, keySpec.Key); overlay != "" {
		return overlay, overlaySource
	}
	if keySpec.Default != "" {
		return keySpec.Default, DefaultSourceCode
	}
	return "", DefaultSourceNone
}

// EnvDefaults 返回指定环境下的完整叠加表（不含 KeySpec.Default）。
// 供测试与运维清单枚举；业务解析请用 ResolveDefault。
func EnvDefaults(env string) map[string]string {
	switch {
	case isProdLike(env):
		return copyStringMap(prodLikeDefaults)
	case isNonProd(env):
		return copyStringMap(nonProdDefaults)
	default:
		return map[string]string{}
	}
}

func envOverlay(env, key string) (value, source string) {
	switch {
	case isProdLike(env):
		if v := prodLikeDefaults[key]; v != "" {
			return v, DefaultSourceProd
		}
	case isNonProd(env):
		if v := nonProdDefaults[key]; v != "" {
			return v, DefaultSourceDev
		}
	}
	return "", ""
}

func isProdLike(env string) bool {
	return env == domain.EnvProd || env == domain.EnvStaging
}

func isNonProd(env string) bool {
	switch env {
	case domain.EnvLocal, domain.EnvDev, domain.EnvTest:
		return true
	default:
		return false
	}
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// prodLikeDefaults 是 staging/prod 的推荐播种叠加层（跨 channel 安全）。
//
// 不写入 iam.mode / identity.enabled 等端别互斥项（由各 cmd 进程补齐）。
// 静态 KeySpec.Default 保持本地友好；本表仅在 prod-like 环境覆盖。
var prodLikeDefaults = map[string]string{
	"db.driver":      "mysql",
	"storage.driver": "s3",
	"async.enabled":  "true",

	"auth.mode":         "jwt",
	"auth.jwt.alg":      "HS256",
	"auth.jwt.issuer":   "admin-frame",
	"auth.jwt.audience": `["admin","biz","c","worker","internal"]`,

	"idempotency.enabled":  "true",
	"idempotency.required": "true",
	"idempotency.driver":   "redis",

	"identity.mfa.admin_required": "true",

	"notification.channels.email.driver": "disabled",
	"notification.channels.sms.driver":   "disabled",
	"notification.channels.push.driver":  "disabled",
}

// nonProdDefaults 是 local/dev/test 的推荐播种叠加层。
//
// 目标：向导与本地一键安装给出「可跑通」的基线，且不把生产禁止项
// （如 auth.mode=demo、storage/db=memory）泄漏到 prod-like。
// 不强制写入 redis.addr：本机未必起 Redis，避免误种后连通性失败。
var nonProdDefaults = map[string]string{
	"auth.mode":         "demo",
	"auth.jwt.alg":      "HS256",
	"auth.jwt.issuer":   "admin-frame",
	"auth.jwt.audience": `["admin","biz","c","worker","internal"]`,

	"log.level": "debug",
}
