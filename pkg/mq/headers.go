package mq

// 标准 Header 键（跨驱动统一；具体 broker 可映射到原生 header）。
const (
	// HeaderMessageID 消息业务幂等键（可与 Message.ID 一致）。
	HeaderMessageID = "mq.message_id"
	// HeaderSchemaVersion 事件 schema 版本，如 "v1"。
	HeaderSchemaVersion = "mq.schema_version"
	// HeaderTraceID 追踪 ID，与 OBS / RequestID 对齐。
	HeaderTraceID = "mq.trace_id"
	// HeaderMerchantID 商户隔离键（B 端必填场景）。
	HeaderMerchantID = "mq.merchant_id"
	// HeaderUserID 用户隔离键（C 端场景）。
	HeaderUserID = "mq.user_id"
	// HeaderContentType 载荷类型，如 application/json。
	HeaderContentType = "mq.content_type"
	// HeaderAttempt 投递次数（部分驱动由 Runner 维护，不依赖 broker）。
	HeaderAttempt = "mq.attempt"
)

// HeaderGet 读取 header；缺失返回 ""。
func HeaderGet(h map[string]string, key string) string {
	if h == nil {
		return ""
	}
	return h[key]
}

// HeaderSet 写入 header（惰性创建 map）。
func HeaderSet(h map[string]string, key, value string) map[string]string {
	if h == nil {
		h = make(map[string]string, 1)
	}
	h[key] = value
	return h
}
