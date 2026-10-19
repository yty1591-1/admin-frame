// Package mq 提供消息生产/消费通用抽象：信封、Producer/Consumer、重试与死信、中间件。
//
// 设计边界（对齐架构 §十五.2 / REL / SEC）：
//   - 不得依赖 internal/；Kafka/NATS 等具体驱动由 platform / infrastructure 实现接口后注入
//   - 投递语义默认至少一次；业务 Handler 必须幂等
//   - 可重试错误回队；耗尽或永久错误进 DLQ（DeadLetter）
//   - 事件须版本化（HeaderSchemaVersion）；载荷含 merchant_id / user_id 等隔离键（见 Headers）
//   - Transactional Outbox 接口见 OutboxStore；持久化实现留业务仓储，本包提供 Memory 便于测试
//   - 禁止在日志中打印完整敏感载荷（SEC-05）
//
// 典型用法：
//
//	broker := mq.NewMemory(mq.MemoryConfig{})
//	_ = broker.Producer().Publish(ctx, mq.Message{Topic: "order.created", Body: payload})
//	_ = broker.Consumer().Subscribe("order.created", mq.Wrap(handler, mq.ConsumeOptions{MaxAttempts: 5}))
//	_ = broker.Consumer().Run(ctx)
package mq
