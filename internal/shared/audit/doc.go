// Package audit 定义跨域共享的审计契约：事件模型、字段字典与 Emitter 端口（AUDIT_STANDARDS）。
//
// 业务在必审路径构造 Event，经 Validate 后交给注入的 Emitter。
// 本包只放类型、校验规则与端口，不放存储或投递实现（无 pkg/mq、pkg/outbox 依赖）；
// 实现位于 internal/platform/audit（SlogEmitter / SQLEmitter / OutboxEmitter），
// 由 cmd 在装配期选择并注入。
//
// 依赖方向：只依赖标准库与内部子文件；不得 import internal/platform 或任何业务域。
package audit
