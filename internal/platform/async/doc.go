// Package async 是平台级通用异步基础设施的装配层：Outbox Store、去重器、路由器、
// Relay 消费与可选 NATS JetStream 传输（ENT-03）。
//
// 边界：本包只装配与投递事件，不定义业务事件类型与处理语义；业务域通过
// Runtime.Register 声明 topic 与 handler。持久化语义由 pkg/outbox 提供。
//
// 依赖方向：async → config、pkg/{db,mq,outbox,metrics}；不得 import 任何业务域。
package async
