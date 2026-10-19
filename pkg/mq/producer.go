package mq

import "context"

// Producer 发布消息到主题（至少一次语义由具体实现保证）。
type Producer interface {
	// Publish 发送单条消息；须尊重 ctx 取消。
	Publish(ctx context.Context, msg Message) error
	// Close 释放资源；可重复调用。
	Close() error
}

// BatchPublisher 可选批量发布能力。
type BatchPublisher interface {
	PublishBatch(ctx context.Context, msgs []Message) error
}
