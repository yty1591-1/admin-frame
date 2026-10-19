// Package validator 提供结构体入参校验：标准/自定义 tag、字段级错误与 i18n 文案。
//
// 设计边界（对齐 API-05 / SEC / 架构 transport 入参校验）：
//   - 不得依赖 internal/；映射业务码 40000 由 apperr / httpx 完成（本包产出 InvalidArgument）
//   - 默认拒绝 JSON 未知字段（DisallowUnknownFields），对齐「拒绝契约外字段」
//   - 校验失败不返回堆栈/内部细节；PublicMessage 面向客户端
//   - i18n 为消息模板层；完整 ICU 可后续替换 Translator
//   - 不替代 OpenAPI 契约；实现须与契约约束一致
//
// 典型用法：
//
//	v := validator.Must()
//	var req CreateOrderRequest
//	if err := v.DecodeJSON(r.Body, &req); err != nil {
//		httpx.FromError(w, err, traceID)
//		return
//	}
package validator
