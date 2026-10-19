// Package httpclient 提供带超时、幂等重试与 OpenTelemetry 出站追踪的 HTTP Client。
//
// 设计边界（对齐 REL-01 / REL-03 / OBS）：
//   - 不得依赖 internal/；禁止业务路径使用 http.DefaultClient / 无超时客户端
//   - 默认 Timeout 强制 >0；下游调用须传入带 deadline 的 context
//   - 默认仅对幂等方法（GET/HEAD/OPTIONS）重试；POST 等须显式 WithRetryNonIdempotent
//   - 重试委托 pkg/retry（次数、退避、抖动）；尊重 Retry-After
//   - 自动注入 W3C traceparent；可选回填 X-Request-ID（来自 TraceID）
//   - 不解析业务 JSON；状态码由调用方处理（Do 对 HTTP 4xx/5xx 仍返回 resp）
//
// 典型用法：
//
//	c, err := httpclient.New(httpclient.Config{Timeout: 5 * time.Second})
//	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
//	resp, err := c.Do(req)
package httpclient
