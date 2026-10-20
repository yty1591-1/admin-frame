// Package apns 提供 Apple Push Notification service 驱动（Token 认证 + HTTP/2）。
//
// 该驱动走 L1 HTTP 协议：标准库实现 ES256 JWT，不引入厂商 SDK。
// 只做一次发送尝试；重试、熔断、限流由调用方负责。
//
// 装配：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/push/apns"
//
//	sender, err := push.Open(ctx, push.Config{Driver: "apns", APNS: push.APNSConfig{...}})
package apns
