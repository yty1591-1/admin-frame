// Package fcm 提供 Firebase Cloud Messaging HTTP v1 驱动（Service Account 认证）。
//
// 该驱动走 L1 HTTP 协议：标准库实现 RS256 JWT 与 OAuth2 Token 缓存，不引入厂商 SDK。
// 只做一次发送尝试；重试、熔断、限流由调用方负责。
//
// 装配：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/push/fcm"
//
//	sender, err := push.Open(ctx, push.Config{Driver: "fcm", FCM: push.FCMConfig{...}})
package fcm
