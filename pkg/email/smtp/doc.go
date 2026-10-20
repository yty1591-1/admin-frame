// Package smtp 提供基于标准库的 SMTP 邮件驱动。
//
// 支持三种加密方式：starttls（默认）、tls（隐式 TLS，常用 465）与 none（仅本地）；
// 支持 plain / login / none 认证。驱动只做一次投递，不重试、不记录 PII。
//
// 装配：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/email/smtp"
//
//	sender, err := email.Open(ctx, email.Config{
//		Driver: "smtp",
//		From:   email.Address{Email: "noreply@example.com"},
//		SMTP:   email.SMTPConfig{Host: "smtp.example.com", Username: "...", Password: "..."},
//	})
package smtp
