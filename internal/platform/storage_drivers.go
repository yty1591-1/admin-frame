package platform

// 按需注册多云对象存储驱动（blank-import）。
// 未使用的驱动仍会进入本 module 的依赖图；若需裁剪可改为按 cmd 分别 import。
import (
	_ "github.com/admin-frame/admin-frame/pkg/storage/cos"
	_ "github.com/admin-frame/admin-frame/pkg/storage/obs"
	_ "github.com/admin-frame/admin-frame/pkg/storage/oss"
	_ "github.com/admin-frame/admin-frame/pkg/storage/s3"
)
