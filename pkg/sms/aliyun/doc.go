// Package aliyun 提供阿里云短信（Dysmsapi）驱动。
//
// 该驱动走 L1 HTTP 协议：使用标准库实现 HMAC-SHA1 签名，不引入厂商 SDK。
// 只做一次发送尝试；重试、熔断、限流由调用方负责。
//
// 装配：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/sms/aliyun"
//
//	sender, err := sms.Open(ctx, sms.Config{Driver: "aliyun", Aliyun: sms.AliyunConfig{...}})
package aliyun
