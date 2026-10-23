package mq

// Broker 组合生产与消费能力（ENT-03 T7 预留扩展点）。
//
// 当前仅有内存实现；DB Outbox 由 pkg/outbox 承担默认驱动。
// NATS JetStream / Kafka 适配在容量触发条件成立后实现，禁止在业务域直接引用具体驱动。
type Broker interface {
	// Producer 返回发布端。
	Producer() Producer
	// Consumer 返回消费端。
	Consumer() Consumer
	// Close 释放底层资源。
	Close() error
}

// 编译期断言：内存 Broker 满足统一接口。
var _ Broker = (*Memory)(nil)
