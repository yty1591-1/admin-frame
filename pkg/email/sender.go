package email

import (
	"context"
	"time"
)

// Sender 发送单封邮件；实现必须并发安全。
//
// Provider 返回驱动名；Send 成功表示服务商已受理，不代表已投递；
// Close 幂等，Close 后 Send 返回包装 ErrClosed 的错误。
type Sender interface {
	// Provider 返回驱动名，如 "smtp"。
	Provider() string
	// Send 发送一封邮件；须尊重 ctx 取消与超时。
	Send(ctx context.Context, msg Message) (Receipt, error)
	// Close 释放资源；可重复调用。
	Close() error
}

// Receipt 是一次「服务商已受理」的凭据；不代表最终投递。
type Receipt struct {
	// Provider 是驱动名。
	Provider string
	// MessageID 是本封邮件的 Message-ID（不含尖括号）。
	MessageID string
	// AcceptedAt 是受理时间，取自注入时钟。
	AcceptedAt time.Time
}
