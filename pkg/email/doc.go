// Package email 提供邮件渠道契约、消息校验、MIME 构造、驱动注册表与可选驱动。
//
// 设计边界：
//   - 不得依赖 internal/；不含模板管理、退订、频控、路由等业务规则
//   - 驱动只做「一次发送尝试」：不重试、不熔断、不写日志
//   - 只负责 MIME 编码与 SMTP 传输；SPF/DKIM/DMARC、退信由 MTA 或邮件服务商承担
//   - Bcc 仅进入 SMTP 信封，绝不写入消息头
//   - 附件默认拒绝可执行扩展名；自定义 Header 仅允许白名单
//   - 收件人、正文、附件内容不得进入日志、Trace、指标标签与错误文本
//   - Sender 实现必须并发安全；Close 幂等，Close 后 Send 返回 ErrClosed
//
// 典型用法：
//
//	sender, err := email.Open(ctx, email.Config{
//		Driver: "smtp",
//		From:   email.Address{Name: "平台", Email: "noreply@example.com"},
//		SMTP:   email.SMTPConfig{Host: "smtp.example.com", Port: 587},
//	})
//	data, messageID, err := email.BuildMIME(msg, email.MIMEOptions{From: cfg.From})
package email
