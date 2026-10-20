// Package all 注册全部云对象存储驱动（AWS S3 兼容、阿里云 OSS、腾讯云 COS、华为云 OBS）。
//
// 进程按需选择导入方式：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/storage/all" // 全部驱动
//	import _ "github.com/admin-frame/admin-frame/pkg/storage/s3"  // 仅 S3/MinIO
//
// 未导入的驱动不会注册，storage.Open 会返回 ErrUnknownDriver；这样可以让
// 不需要多云能力的进程（如 cmd/migrate）不链接任何云 SDK（PLAT-10）。
package all

// 下列驱动包仅用于 init 注册到存储工厂；本包不直接引用其符号。
import (
	// 腾讯云 COS。
	_ "github.com/admin-frame/admin-frame/pkg/storage/cos"
	// 华为云 OBS。
	_ "github.com/admin-frame/admin-frame/pkg/storage/obs"
	// 阿里云 OSS。
	_ "github.com/admin-frame/admin-frame/pkg/storage/oss"
	// AWS S3 / MinIO / S3 兼容。
	_ "github.com/admin-frame/admin-frame/pkg/storage/s3"
)
