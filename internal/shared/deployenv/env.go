// Package deployenv 提供部署环境名判定，供 transport / app / platform 共用。
//
// 放在 shared 而非 platform/config：transport 禁止依赖 platform（LAYER-01），
// 但 CC-R 守卫需要同一套 prod-like / local-dev 判定。
package deployenv

import "strings"

// IsProd 报告是否为生产环境。
func IsProd(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), "prod")
}

// IsProdLike 报告环境是否按生产标准校验（prod / staging）。
func IsProdLike(env string) bool {
	return IsProd(env) || strings.EqualFold(strings.TrimSpace(env), "staging")
}

// IsLocalDev 报告是否为允许本地开发回退的环境（local / dev / test）。
//
// staging/prod 以及未声明（空）一律返回 false，避免漏注入 Env 时误开源码内
// 固定密钥（CC-R-02）。
func IsLocalDev(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "test":
		return true
	default:
		return false
	}
}
