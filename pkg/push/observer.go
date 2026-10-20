package push

import (
	"context"
	"time"
)

// Event 是一次发送尝试的只读快照。
//
// 禁止包含 Token、标题、正文与 Data；Platform 为低基数标签。
type Event struct {
	// Channel 固定为 "push"。
	Channel string
	// Provider 是驱动名。
	Provider string
	// Platform 是目标平台。
	Platform string
	// Operation 为 "send"。
	Operation string
	// Result 为 "success" 或 "failure"。
	Result string
	// Code 是 pkg/errors 语义码（小写）；成功时为空。
	Code string
	// RetryAfter 是服务商建议等待时间；0 表示未提供。
	RetryAfter time.Duration
	// Duration 是本次尝试耗时。
	Duration time.Duration
}

// Observer 接收发送事件；由 platform 实现日志/指标/Trace。
//
// 实现必须并发安全且快速返回；不得记录 Token 或推送内容。
type Observer interface {
	Observe(ctx context.Context, ev Event)
}

func observe(ctx context.Context, obs Observer, ev Event) {
	if obs == nil {
		return
	}
	ev.Channel = "push"
	obs.Observe(ctx, ev)
}
