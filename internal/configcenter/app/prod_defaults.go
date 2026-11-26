package app

import (
	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

// resolveSeedValue 解析播种取值：Overrides > 环境推荐默认值（spec.ResolveDefault）。
//
// 环境推荐表声明在 internal/configcenter/spec（CFG-02 单一事实源），
// 本函数只做 Overrides 优先合并，避免 app 与 spec 各维护一份叠加层。
func resolveSeedValue(env string, keySpec domain.KeySpec, overrides map[string]string) string {
	if overrides != nil {
		if v, ok := overrides[keySpec.Key]; ok && v != "" {
			return v
		}
	}
	value, _ := spec.ResolveDefault(env, keySpec)
	return value
}
