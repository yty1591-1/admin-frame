// Package mask 提供日志 / 响应场景的 PII 与密钥脱敏原语（SEC-05 / AUD-08 / OBS）。
//
// 设计边界：
//   - 不得依赖 internal/；不替代分端 DTO 裁剪（SEC-06）或审计禁写策略（AUD-08）
//   - 按 rune 处理中文姓名/证件；空串与过短输入安全回落（全掩或原样策略见各函数）
//   - 自由文本 Text 为启发式扫描，不能保证 100% 召回；关键字段应显式调用 Phone / IDCard 等
//   - 脱敏结果仅用于日志、Trace、审计摘要、对外展示；不得当作可逆加密
//
// 典型用法：
//
//	slog.Info("login", "mobile", mask.Phone("13812345678"))
//	slog.Info("payload", "body", mask.Text(raw))
//	attrs := mask.Map(map[string]string{"phone": mobile, "token": tok})
package mask
