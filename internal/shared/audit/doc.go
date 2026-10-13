// Package audit 提供生产级审计事件模型与 Emitter（AUDIT_STANDARDS）。
//
// 业务在必审路径构造 Event，经 Validate 后交给 Emitter。
// 脚手架默认 SlogEmitter；生产应替换为追加写存储或可靠投递实现。
package audit
