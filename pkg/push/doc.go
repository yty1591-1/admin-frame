// Package push 提供移动推送渠道契约、消息校验、驱动注册表与可选驱动。
//
// 设计边界：
//   - 不得依赖 internal/；不含模板、路由、去重、频控等业务规则
//   - 驱动只做「一次发送尝试」：不重试、不熔断、不写日志
//   - Token、推送内容不得进入日志、Trace、指标标签与错误文本
//   - 载荷超过平台上限时在触网前返回 ErrInvalidMessage
//   - Sender 实现必须并发安全；Close 幂等，Close 后 Send 返回 ErrClosed
//
// 典型用法：
//
//	sender, err := push.Open(ctx, push.Config{
//		Driver: "apns",
//		APNS:   push.APNSConfig{Topic: "com.example.app", KeyID: "...", TeamID: "...", PrivateKey: keyPEM},
//	})
//	_, err = sender.Send(ctx, push.Message{
//		Token: token, Platform: push.PlatformIOS, Title: "订单更新", Body: "已发货",
//	})
package push
