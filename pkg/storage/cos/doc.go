// Package cos 提供腾讯云 COS 驱动（可选项）。
//
// 默认 L1：S3 兼容协议（复用 storage/s3）。
// L2：cos.native=true 时使用 github.com/tencentyun/cos-go-sdk-v5。
//
//	import _ "github.com/admin-frame/admin-frame/pkg/storage/cos"
package cos
