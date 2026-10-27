package domain

import "context"

// MenuRepository 是菜单 / 路由资源的只读持久化端口。
//
// 菜单由代码种子维护，接口不提供写入；实现必须按端别过滤并按 sort、code 稳定排序。
type MenuRepository interface {
	// List 返回指定端别下满足状态的菜单，按 sort、code 升序。
	// status 为空表示不过滤状态。
	List(ctx context.Context, channel Channel, status MenuStatus) ([]*Menu, error)
}
