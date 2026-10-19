package mq

import "context"

// Handler 处理单条消息；返回 nil 表示成功确认（Ack）。
//
// 错误分类（见 Wrap / Classify）：
//   - 可重试 → 重新投递（Attempt+1），未超 MaxAttempts
//   - 永久失败或超次 → 投递 DLQ（若配置）后 Ack 原消息，避免毒丸阻塞
type Handler func(ctx context.Context, msg Message) error

// Consumer 订阅主题并拉取/推送消息。
type Consumer interface {
	// Subscribe 注册主题处理器；同一主题可多次订阅（扇出），实现定义语义。
	Subscribe(topic string, h Handler) error
	// Run 阻塞消费直至 ctx 取消或 Close；须可优雅退出。
	Run(ctx context.Context) error
	// Close 停止消费并释放资源。
	Close() error
}
