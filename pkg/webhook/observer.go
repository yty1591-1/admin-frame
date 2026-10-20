package webhook

import (
	"context"
	"time"
)

// Event 是一次验签尝试的只读快照。
//
// 禁止包含消息 ID、请求体、签名、密钥与请求头原值；字段必须保持低基数，
// 可直接作为指标标签。
type Event struct {
	// Channel 固定为 "webhook"。
	Channel string
	// Operation 固定为 "verify"（预留给后续 sign 等操作）。
	Operation string
	// Version 是签名版本标识，如 "v1"。
	Version string
	// Result 为 "success" 或 "failure"。
	Result string
	// Code 是 pkg/errors 语义码（小写）；成功时为空。
	Code string
	// Replay 表示失败原因是重放命中。
	Replay bool
	// Duration 是本次校验耗时。
	Duration time.Duration
}

// Observer 接收校验事件；由 platform 实现日志/指标/Trace。
//
// 实现必须并发安全且快速返回；不应阻塞请求路径，也不得在实现中记录 PII。
type Observer interface {
	Observe(ctx context.Context, ev Event)
}

func observe(ctx context.Context, obs Observer, ev Event) {
	if obs == nil {
		return
	}
	ev.Channel = "webhook"
	obs.Observe(ctx, ev)
}
