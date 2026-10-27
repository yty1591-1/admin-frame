// Package menuview 维护菜单可引用的前端页面清单。
//
// 菜单的 component 会被前端 `generateRoutesByBackend` 映射到 views 下的 .vue 文件；
// 拼错或指向不存在的页面只会在运行时表现为 404，因此写入前必须在此白名单内校验。
// 白名单与前端文件的一致性由 menuview_test 通过扫描仓库内 views 目录保证。
package menuview

import (
	"slices"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// adminComponents 是 A 端允许登记的页面组件（以 views 目录为根的绝对路径）。
//
// 新增页面时在此追加，并在同一变更中创建对应 .vue 文件，否则测试失败。
var adminComponents = []string{
	"/system/role/index.vue",
}

// Catalog 是 ComponentCatalog 的默认实现。
type Catalog struct{}

// Allows 判断指定端别是否允许引用该 component。
func (Catalog) Allows(channel domain.Channel, component string) bool {
	if channel != domain.ChannelAdmin {
		return false
	}
	return slices.Contains(adminComponents, component)
}

// Components 返回指定端别允许的 component 清单副本，供管理端表单渲染。
func (Catalog) Components(channel domain.Channel) []string {
	if channel != domain.ChannelAdmin {
		return nil
	}
	out := make([]string, len(adminComponents))
	copy(out, adminComponents)
	return out
}
