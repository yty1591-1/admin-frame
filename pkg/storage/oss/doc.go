// Package oss 提供阿里云 OSS 驱动（可选项）。
//
// 默认 L1：S3 兼容协议（复用 storage/s3）。
// L2：oss.native=true 时使用 github.com/aliyun/alibabacloud-oss-go-sdk-v2。
//
//	import _ "github.com/admin-frame/admin-frame/pkg/storage/oss"
package oss
