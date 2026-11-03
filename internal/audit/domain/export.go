package domain

import (
	"context"
	"errors"
	"time"
)

// ErrExportNotFound 表示导出任务不存在（或不属于调用方）。
//
// 上层统一映射为 404：不区分「不存在」与「无权查看」，避免探测他人导出。
var ErrExportNotFound = errors.New("audit export job not found")

// ExportStatus 是导出任务状态。
type ExportStatus string

const (
	// ExportPending 已创建，等待 worker 执行。
	ExportPending ExportStatus = "pending"
	// ExportRunning 已被某个 worker 抢占，正在生成文件。
	ExportRunning ExportStatus = "running"
	// ExportSucceeded 文件已上传，可下载。
	ExportSucceeded ExportStatus = "succeeded"
	// ExportFailed 重试耗尽，不再自动执行。
	ExportFailed ExportStatus = "failed"
	// ExportExpired 超过保留期，文件已删除。
	ExportExpired ExportStatus = "expired"
)

// Valid 判断状态是否为已知枚举；用于读取配置与数据库行时的一致性校验。
func (s ExportStatus) Valid() bool {
	switch s {
	case ExportPending, ExportRunning, ExportSucceeded, ExportFailed, ExportExpired:
		return true
	default:
		return false
	}
}

// Terminal 表示状态不会再变化。
func (s ExportStatus) Terminal() bool {
	switch s {
	case ExportSucceeded, ExportFailed, ExportExpired:
		return true
	default:
		return false
	}
}

// FilterSnapshot 是导出过滤条件的持久化形态。
//
// 时间统一存 RFC3339（UTC），避免不同驱动的时区解析差异；Limit/Offset 不入库，
// 执行时按 audit.export.max_rows 重新收敛。
type FilterSnapshot struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	ActorID      string    `json:"actor_id,omitempty"`
	Action       string    `json:"action,omitempty"`
	Channel      string    `json:"channel,omitempty"`
	Outcome      string    `json:"outcome,omitempty"`
	MerchantID   string    `json:"merchant_id,omitempty"`
	ResourceType string    `json:"resource_type,omitempty"`
	ResourceID   string    `json:"resource_id,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
}

// Snapshot 把查询条件转为可持久化快照。
func Snapshot(filter Filter) FilterSnapshot {
	return FilterSnapshot{
		From:         filter.From.UTC(),
		To:           filter.To.UTC(),
		ActorID:      filter.ActorID,
		Action:       filter.Action,
		Channel:      filter.Channel,
		Outcome:      filter.Outcome,
		MerchantID:   filter.MerchantID,
		ResourceType: filter.ResourceType,
		ResourceID:   filter.ResourceID,
		TraceID:      filter.TraceID,
	}
}

// Filter 还原为查询条件；limit 由执行方按配置给定。
func (s FilterSnapshot) Filter(limit int) Filter {
	return Filter{
		From:         s.From,
		To:           s.To,
		ActorID:      s.ActorID,
		Action:       s.Action,
		Channel:      s.Channel,
		Outcome:      s.Outcome,
		MerchantID:   s.MerchantID,
		ResourceType: s.ResourceType,
		ResourceID:   s.ResourceID,
		TraceID:      s.TraceID,
		Limit:        limit,
	}
}

// ExportJob 是一次异步导出任务。
type ExportJob struct {
	// ID 任务标识，同时作为下载资源标识。
	ID string
	// ActorID 发起导出的主体；只有本人可以查询与下载。
	ActorID string
	// ActorType 主体类型，如 admin。
	ActorType string
	// Channel 端别。
	Channel string
	// MerchantID B 端商户 ID，可空。
	MerchantID string
	// TraceID 创建请求的追踪 ID。
	TraceID string
	// Filter 导出范围与过滤条件。
	Filter FilterSnapshot
	// Status 当前状态。
	Status ExportStatus
	// Attempts 已执行次数。
	Attempts int
	// RowCount 成功导出的行数。
	RowCount int
	// ObjectKey 对象存储键；成功后才非空。
	ObjectKey string
	// LastError 最近一次失败原因。
	LastError string
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// StartedAt 最后一次开始执行时间；零值表示从未执行。
	StartedAt time.Time
	// FinishedAt 进入终态时间；零值表示仍在进行。
	FinishedAt time.Time
	// ExpiresAt 文件与记录保留截止时间。
	ExpiresAt time.Time
}

// Downloadable 判断当前是否可下载：仅成功且未过期。
func (j ExportJob) Downloadable(now time.Time) bool {
	return j.Status == ExportSucceeded && now.Before(j.ExpiresAt)
}

// Expired 判断是否已到保留期截止时间（无论当前状态）。
func (j ExportJob) Expired(now time.Time) bool {
	return !j.ExpiresAt.IsZero() && !now.Before(j.ExpiresAt)
}

// ExportStore 是导出任务的持久化契约。
type ExportStore interface {
	// Create 写入新任务（pending）。
	Create(ctx context.Context, job ExportJob) error
	// Get 按 ID 读取任务；不存在返回 ErrExportNotFound。
	Get(ctx context.Context, id string) (ExportJob, error)
	// ClaimPending 抢占最早的一条 pending 任务并置为 running。
	//
	// 返回 found=false 表示当前没有待执行任务。多副本安全由行锁 + SKIP LOCKED 保证。
	ClaimPending(ctx context.Context, now time.Time) (job ExportJob, found bool, err error)
	// RequeueStale 回收 started_at 早于 cutoff 的 running 任务（worker 崩溃恢复）。
	//
	// 已执行 maxAttempts 次的任务不再排队而是置为 failed：否则一个反复让
	// worker 崩溃的任务会永远是最早的 pending，饿死后面所有导出。
	RequeueStale(ctx context.Context, cutoff time.Time, maxAttempts int) (int, error)
	// MarkSucceeded 置为成功并记录行数与对象键。
	MarkSucceeded(ctx context.Context, id string, rows int, objectKey string, finishedAt time.Time) error
	// MarkFailed 记录一次失败：canRetry 为 true 时回到 pending，否则置 failed。
	MarkFailed(ctx context.Context, id, reason string, finishedAt time.Time, canRetry bool) error
	// ListExpired 返回 expires_at 早于 now 且尚未置为 expired 的任务。
	ListExpired(ctx context.Context, now time.Time, limit int) ([]ExportJob, error)
	// MarkExpired 批量置为 expired。
	MarkExpired(ctx context.Context, ids []string, finishedAt time.Time) error
}
