package app

import (
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
)

// prodLikeSeedDefaults 返回 prod/staging 播种叠加值（跨 channel 安全）。
//
// 不覆盖静态 KeySpec.Default：仅 EnsureBaseline / reconcile 在 prod-like
// 环境解析时优先使用。iam.mode / identity.enabled 等端别互斥项不在此表，
// 由各 cmd 进程补齐。
func prodLikeSeedDefaults() map[string]string {
	return map[string]string{
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
}

// resolveSeedValue 解析播种取值：Overrides > prod-like overlay > KeySpec.Default。
func resolveSeedValue(env string, keySpec domain.KeySpec, overrides map[string]string) string {
	if overrides != nil {
		if v, ok := overrides[keySpec.Key]; ok && v != "" {
			return v
		}
	}
	if platformconfig.IsProdLikeEnv(env) {
		if overlay, ok := prodLikeSeedDefaults()[keySpec.Key]; ok && overlay != "" {
			return overlay
		}
	}
	return keySpec.Default
}
