package platform

// 按需注册多云对象存储驱动（blank-import）。
// 未使用的驱动仍会进入本 module 的依赖图；若需裁剪可改为按 cmd 分别 import。
import (
	// 注册 COS 驱动。
	_ "github.com/admin-frame/admin-frame/pkg/storage/cos"
	// 注册 OBS 驱动。
	_ "github.com/admin-frame/admin-frame/pkg/storage/obs"
	// 注册 OSS 驱动。
	_ "github.com/admin-frame/admin-frame/pkg/storage/oss"
	// 注册 S3 兼容驱动。
	_ "github.com/admin-frame/admin-frame/pkg/storage/s3"
)
