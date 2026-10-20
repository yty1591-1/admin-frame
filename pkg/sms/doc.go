// Package sms 提供短信渠道契约、消息校验、驱动注册表与可选驱动。
//
// 设计边界：
//   - 不得依赖 internal/；不含模板管理、频控、路由、退订等业务规则
//   - 驱动只做「一次发送尝试」：不重试、不熔断、不限流、不写日志
//   - 重试/熔断/限流由调用方用 pkg/retry、pkg/circuitbreaker、pkg/ratelimit 组合
//   - 错误统一映射为 pkg/errors 语义码，可被 pkg/retry 默认 RetryIf 识别
//   - 手机号、验证码、模板参数不得进入日志、Trace、指标标签与错误文本
//   - Sender 实现必须并发安全；Close 幂等，Close 后 Send 返回 ErrClosed
//
// 典型用法：
//
//	sender, err := sms.Open(ctx, sms.Config{
//		Driver:  "aliyun",
//		Timeout: 3 * time.Second,
//		Aliyun:  sms.AliyunConfig{Region: "cn-hangzhou", SignName: "某某科技"},
//	})
//	if err != nil {
//		return err
//	}
//	defer sender.Close()
//
//	_, err = sender.Send(ctx, sms.Message{
//		Phone:      "+8613800138000",
//		TemplateID: "SMS_123456789",
//		Params:     map[string]string{"code": "1234"},
//	})
package sms
