// Package logger 提供结构化 slog 装配、OBS 字段约定与 context 关联（trace / request）。
//
// 设计边界（对齐 OBS-01 / SEC-05 / 架构日志选型）：
//   - 不得依赖 internal/；进程级 service/channel/env 由调用方注入
//   - 生产默认 JSON；text 仅限 local/dev
//   - ContextHandler 在 InfoContext 等调用时自动附加 trace_id / span_id
//   - 关联键 WithTraceID 与响应信封 / X-Request-ID 对齐（OBS-05）；优先于裸 OTel TraceID
//   - 禁止记录密钥、完整 Token、未脱敏 PII（SEC-05）；脱敏见 pkg/mask
//   - HTTP AccessLog / Recover 等中间件仍留 platform；本包只提供 Logger 原语
//
// 典型用法：
//
//	log := logger.New(logger.Config{Service: "admin-api", Channel: "admin", Env: "dev"})
//	ctx = logger.WithTraceID(ctx, requestID)
//	log.InfoContext(ctx, "order created", "order_id", id)
package logger
