package rest

import "time"

// EventDTO 是审计事件响应项（AUD-04 字段全集）。
type EventDTO struct {
	EventID      string            `json:"event_id"`
	Action       string            `json:"action"`
	ActorID      string            `json:"actor_id"`
	ActorType    string            `json:"actor_type,omitempty"`
	Channel      string            `json:"channel"`
	MerchantID   string            `json:"merchant_id,omitempty"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Outcome      string            `json:"outcome"`
	OccurredAt   time.Time         `json:"occurred_at"`
	TraceID      string            `json:"trace_id"`
	Service      string            `json:"service"`
	Reason       string            `json:"reason,omitempty"`
	Attrs        map[string]string `json:"attrs,omitempty"`
}

// ListResponse 是审计事件分页响应。
type ListResponse struct {
	Items []EventDTO `json:"items"`
	Total int        `json:"total"`
}
