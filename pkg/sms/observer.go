package sms

import (
	"context"
	"time"
)

// Event 是一次发送尝试的只读快照。
//
// 禁止包含手机号、正文、模板参数与凭据；Template 必须是低基数模板键。
type Event struct {
	// Channel 固定为 "sms"。
	Channel string
	// Provider 是驱动名。
	Provider string
	// Operation 为 "send" 或 "send_batch"。
	Operation string
	// Template 是低基数模板键；裸内容时为空。
	Template string
	// Result 为 "success" 或 "failure"。
	Result string
	// Code 是 pkg/errors 语义码（小写）；成功时为空。
	Code string
	// RetryAfter 是厂商建议等待时间；0 表示未提供。
	RetryAfter time.Duration
	// Duration 是本次尝试耗时。
	Duration time.Duration
}

// Observer 接收发送事件；由 platform 实现日志/指标/Trace。
//
// 实现必须并发安全且快速返回；不应阻塞发送路径，也不得在实现中记录 PII。
type Observer interface {
	Observe(ctx context.Context, ev Event)
}

func observe(ctx context.Context, obs Observer, ev Event) {
	if obs == nil {
		return
	}
	ev.Channel = "sms"
	obs.Observe(ctx, ev)
}
