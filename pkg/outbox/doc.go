// Package outbox 提供平台级事务性 Outbox 与消费运行时（ENT-03）。
//
// 设计边界：
//   - 不依赖 internal/；MySQL 实现直接使用 database/sql；
//   - 投递语义为至少一次：认领使用条件更新 + 租约回收，消费方必须幂等；
//   - 失败按指数退避重试，永久失败或重试耗尽进入死信（async_dlq）；
//   - 业务域只声明事件类型与 Handler，不重复实现可靠性逻辑；
//   - Broker 扩展点见 pkg/mq.Broker；默认 driver=db，不引入外部 MQ 依赖。
package outbox
