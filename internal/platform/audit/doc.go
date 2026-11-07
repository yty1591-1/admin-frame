// Package audit 提供审计事件的落库与投递实现（AUD-03 / AUD-06 / AUD-07）。
//
// 边界：事件模型（Event）、字段字典（actions）、校验规则与 Emitter 端口定义在
// internal/shared/audit（契约层）；本包只承载实现——结构化日志出口、追加写审计表、
// 事务内 Outbox 投递。契约层不得反向依赖本包，避免所有引用 Event 的域被拖入
// 异步与存储依赖。
//
// 依赖方向：platform/audit → shared/audit、shared/apperr、pkg/{mq,outbox,retry}；
// 不得 import 任何业务域，也不得被 internal/shared 引用。
package audit
