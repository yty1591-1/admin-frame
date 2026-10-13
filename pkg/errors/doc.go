// Package errors 提供生产级错误原语：稳定错误码、因果链、调用栈、
// 结构化字段、可重试标记，以及对外消息与内部详情分离。
//
// 设计目标：
//   - 与标准库 errors.Is / errors.As / errors.Unwrap / errors.Join 兼容
//   - 无业务域依赖，可被多个服务复用
//   - 创建点捕获栈；包装已有 *Error 时不重复打栈
//   - 对外 Message 可安全返回客户端；Detail 仅供日志/排障
//
// 典型用法：
//
//	err := errors.New("order not found").
//		WithCode(errors.CodeNotFound).
//		WithOp("order.Get").
//		WithField("order_id", id)
//
//	if errors.IsCode(err, errors.CodeNotFound) { ... }
//
// 业务侧稳定数字错误码（如 40400）应定义在 internal/shared/apperr，
// 本包提供可移植的语义 Code（字符串），便于跨服务与 gRPC 映射。
package errors
