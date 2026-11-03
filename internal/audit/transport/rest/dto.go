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

// ExportJobDTO 是异步导出任务的状态响应。
type ExportJobDTO struct {
	// ExportID 任务 ID，同时是下载资源标识。
	ExportID string `json:"export_id"`
	// Status pending / running / succeeded / failed / expired。
	Status string `json:"status"`
	// Rows 成功导出的行数；未完成时为 0。
	Rows int `json:"rows"`
	// CreatedAt 创建时间（UTC）。
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt 文件保留截止时间（UTC）。
	ExpiresAt time.Time `json:"expires_at"`
	// FinishedAt 进入终态时间（UTC）；进行中不返回。
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// DownloadPath 可下载时的相对路径（需 audit:read）；否则为空。
	DownloadPath string `json:"download_path,omitempty"`
	// Error 终态失败原因（截断），成功时不返回。
	Error string `json:"error,omitempty"`
}
