package sms

import (
	"context"
	"time"
)

// Sender 发送单条短信；实现必须并发安全。
//
// Provider 返回驱动名（用于日志与指标，低基数）；Send 成功仅表示厂商已受理，
// 不代表已送达；Close 幂等，Close 后 Send 返回包装 ErrClosed 的错误。
type Sender interface {
	// Provider 返回驱动名，如 "aliyun"。
	Provider() string
	// Send 发送一条短信；须尊重 ctx 取消与超时。
	Send(ctx context.Context, msg Message) (Receipt, error)
	// Close 释放资源；可重复调用。
	Close() error
}

// Receipt 是一次「厂商已受理」的凭据；不代表最终送达。
type Receipt struct {
	// Provider 是驱动名。
	Provider string
	// MessageID 是厂商消息 ID；SMTP 类渠道可为空。
	MessageID string
	// AcceptedAt 是受理时间，取自注入时钟。
	AcceptedAt time.Time
}

// BatchSender 是支持批量发送的可选能力；通过 AsBatchSender 探测。
type BatchSender interface {
	// SendBatch 逐条返回结果；Errors 与 msgs 等长，nil 表示该条已受理。
	SendBatch(ctx context.Context, msgs []Message) (BatchResult, error)
}

// BatchResult 与输入等长；Errors[i] 为 nil 表示第 i 条已受理。
type BatchResult struct {
	// Receipts 与输入等长；失败位置为零值。
	Receipts []Receipt
	// Errors 与输入等长；nil 表示成功。
	Errors []error
}

// Failed 返回失败条数。
func (r BatchResult) Failed() int {
	n := 0
	for _, err := range r.Errors {
		if err != nil {
			n++
		}
	}
	return n
}

// AsBatchSender 探测批量能力；s 为 nil 时返回 (nil, false)。
func AsBatchSender(s Sender) (BatchSender, bool) {
	if s == nil {
		return nil, false
	}
	b, ok := s.(BatchSender)
	return b, ok
}
