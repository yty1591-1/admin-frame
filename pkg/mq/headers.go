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
	// HeaderEventID 全局唯一事件 ID，同时作为消费幂等键默认值（ENT-03）。
	HeaderEventID = "mq.event_id"
	// HeaderEventType 事件类型，消费方按此路由（ENT-03）。
	HeaderEventType = "mq.event_type"
	// HeaderEventVersion 事件 schema 版本（ENT-03），与 HeaderSchemaVersion 同步写入。
	HeaderEventVersion = "mq.event_version"
	// HeaderTenantID 租户隔离键（ENT-03）。
	HeaderTenantID = "mq.tenant_id"
	// HeaderOccurredAt 业务发生时间（RFC3339Nano，ENT-03）。
	HeaderOccurredAt = "mq.occurred_at"
	// HeaderIdempotencyKey 业务幂等键；缺省时消费方回退 HeaderEventID（ENT-03）。
	HeaderIdempotencyKey = "mq.idempotency_key"
)

// DefaultEventVersion 是未显式声明 schema 版本时的默认值。
const DefaultEventVersion = "v1"

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
