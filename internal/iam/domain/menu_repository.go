package domain

import "context"

// MenuRepository 是菜单 / 路由资源的持久化端口。
//
// 读路径按端别过滤并按 sort、code 稳定排序；写路径仅由 A 端管理用例调用，
// 且必须使用 version 做乐观锁。
type MenuRepository interface {
	// List 返回指定端别下满足状态的菜单，按 sort、code 升序。
	// status 为空表示不过滤状态。
	List(ctx context.Context, channel Channel, status MenuStatus) ([]*Menu, error)
	// Get 按主键加载单条菜单；不存在时返回 40400。
	Get(ctx context.Context, id string) (*Menu, error)
	// Create 写入新菜单；编码或路由名冲突返回 40900。
	Create(ctx context.Context, menu *Menu) error
	// Update 以乐观锁保存菜单；expectedVersion 不匹配返回 40900。
	Update(ctx context.Context, menu *Menu, expectedVersion int64) error
}
