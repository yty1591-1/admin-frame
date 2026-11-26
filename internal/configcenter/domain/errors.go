package domain

import "errors"

// 仓储层哨兵错误；应用层据此映射 HTTP 409/404（M1-4）。
var (
	// ErrNotFound 表示目标记录不存在。
	ErrNotFound = errors.New("configcenter: not found")
	// ErrDuplicateKey 表示唯一键冲突（item 三元组或 release_no）。
	ErrDuplicateKey = errors.New("configcenter: duplicate key")
	// ErrVersionConflict 表示乐观锁版本不匹配。
	ErrVersionConflict = errors.New("configcenter: version conflict")
	// ErrStateConflict 表示状态迁移的前置状态已变化。
	ErrStateConflict = errors.New("configcenter: state conflict")
	// ErrPointerConflict 表示发布指针 CAS 失败（被并发切换）。
	ErrPointerConflict = errors.New("configcenter: release pointer conflict")
)
