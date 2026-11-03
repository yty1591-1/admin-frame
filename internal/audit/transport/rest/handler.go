// Package rest 提供审计查询与导出的 HTTP 传输实现（/admin/v1/audit/*）。
//
// 只做参数解析、用例调用与响应封装；不感知仓储实现，也不在 transport 内
// 触发审计写入（导出审计由 app.QueryService 负责，保持用例边界）。
package rest

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/app"
	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/internal/shared/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// HandlerConfig 是审计 Handler 的装配参数。
type HandlerConfig struct {
	// Query 审计查询与导出用例，必填。
	Query *app.QueryService
	// Jobs 异步导出用例；nil 表示 audit.export.async 未启用，相关路由返回 503。
	Jobs *app.ExportJobService
	// QueryLimits 与配置 audit.query 一致，用于分页参数解析。
	QueryLimits pagination.Config
	// MaxRange 查询允许的最大时间跨度（audit.query.max_range_days）。
	MaxRange time.Duration
	// ExportMaxRange 导出允许的最大时间跨度（audit.export.max_range_days）。
	ExportMaxRange time.Duration
	// Channel 端别，用于校验 Principal 与处理器一致（SEC-14）。
	Channel string
	// Service 产生审计事件的服务名，写入 data.export 事件。
	Service string
}

// Handler 处理审计查询与导出请求。
type Handler struct {
	query          *app.QueryService
	jobs           *app.ExportJobService
	pageCfg        pagination.Config
	maxRange       time.Duration
	exportMaxRange time.Duration
	channel        string
	service        string
}

// NewHandler 构造审计 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Query == nil {
		return nil, apperr.Internal("audit handler requires a query service", nil)
	}
	if strings.TrimSpace(cfg.Channel) == "" {
		return nil, apperr.Internal("audit handler requires a channel", nil)
	}
	return &Handler{
		query:          cfg.Query,
		jobs:           cfg.Jobs,
		pageCfg:        cfg.QueryLimits,
		maxRange:       cfg.MaxRange,
		exportMaxRange: cfg.ExportMaxRange,
		channel:        cfg.Channel,
		service:        cfg.Service,
	}, nil
}

// List 处理 GET /admin/v1/audit/events。
//
// 时间窗必填（from / to，RFC3339）：无时间窗的审计查询会退化为全表扫描。
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	page, err := pagination.ParseWith(r, h.pageCfg)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	filter, err := parseFilter(r, page.Limit(), page.Offset())
	if err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	if err := filter.Validate(h.maxRange); err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	result, err := h.query.List(r.Context(), filter, h.accessMeta(principal, traceID))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	items := make([]EventDTO, 0, len(result.Items))
	for _, event := range result.Items {
		items = append(items, toEventDTO(event))
	}
	httpx.OK(w, ListResponse{Items: items, Total: result.Total}, traceID)
}

// Export 处理 GET /admin/v1/audit/events/export，以 CSV 流式返回。
//
// 导出前先做全量校验（时间窗、范围、操作者），校验通过后才写响应头，
// 避免把 JSON 错误写进 CSV 流。
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	// 导出由用例统一把 limit 收敛到 audit.export.max_rows，这里给 1 只为通过
	// 「limit 必填」这一与查询共用的校验，不代表实际导出条数。
	filter, err := parseFilter(r, 1, 0)
	if err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	if err := filter.Validate(h.exportMaxRange); err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	meta := h.accessMeta(principal, traceID)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-events.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := h.query.Export(r.Context(), filter, meta, w); err != nil {
		// 已开始写响应流时无法改状态码；记录错误交由结构化日志与告警发现。
		httpx.FromError(w, err, traceID)
		return
	}
}

// CreateExport 处理 POST /admin/v1/audit/events/export，创建异步导出任务。
//
// 与同步 GET 共用过滤参数与上限校验；区别只在「立刻返回 CSV 流」还是
// 「落任务行，由 worker 生成文件」。返回 202 + 任务状态，客户端轮询
// /admin/v1/audit/exports/{id} 直到 status=succeeded 再下载。
func (h *Handler) CreateExport(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.jobs == nil {
		httpx.FromError(w, apperr.Unavailable("async audit export is disabled"), traceID)
		return
	}
	filter, err := parseFilter(r, 1, 0)
	if err != nil {
		httpx.FromError(w, apperr.BadRequest(err.Error()), traceID)
		return
	}
	job, err := h.jobs.Create(r.Context(), filter, h.accessMeta(principal, traceID))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.JSON(w, http.StatusAccepted, 0, "accepted", toExportJobDTO(job), traceID)
}

// GetExport 处理 GET /admin/v1/audit/exports/{id}，返回任务状态。
func (h *Handler) GetExport(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.jobs == nil {
		httpx.FromError(w, apperr.Unavailable("async audit export is disabled"), traceID)
		return
	}
	job, err := h.jobs.Get(r.Context(), r.PathValue("id"), h.accessMeta(principal, traceID))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	httpx.OK(w, toExportJobDTO(job), traceID)
}

// DownloadExport 处理 GET /admin/v1/audit/exports/{id}/download，返回 CSV。
//
// 先取到完整字节再写响应头：任务未完成 / 已过期时仍能返回 JSON 错误，
// 不会出现「已经写了 200 和 CSV 头，才在流中间报错」的半截文件。
func (h *Handler) DownloadExport(w http.ResponseWriter, r *http.Request) {
	traceID := httpx.TraceIDFromContext(r.Context())
	principal, err := h.principal(r)
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	if h.jobs == nil {
		httpx.FromError(w, apperr.Unavailable("async audit export is disabled"), traceID)
		return
	}
	job, body, err := h.jobs.Open(r.Context(), r.PathValue("id"), h.accessMeta(principal, traceID))
	if err != nil {
		httpx.FromError(w, err, traceID)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-events-%s.csv"`, job.ID))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// accessMeta 把主体与链路信息转换为访问审计元数据。
func (h *Handler) accessMeta(principal auth.Principal, traceID string) app.AccessMeta {
	return app.AccessMeta{
		ActorID:    principal.SubjectID,
		ActorType:  "admin",
		Channel:    h.channel,
		MerchantID: principal.MerchantID,
		TraceID:    traceID,
	}
}

// principal 从上下文取当前主体并校验端别一致。
func (h *Handler) principal(r *http.Request) (auth.Principal, error) {
	principal, ok := auth.FromContext(r.Context())
	if !ok || strings.TrimSpace(principal.SubjectID) == "" {
		return auth.Principal{}, apperr.Unauthorized("authentication required")
	}
	if string(principal.Channel) != h.channel {
		return auth.Principal{}, apperr.Unauthorized("channel mismatch")
	}
	return principal, nil
}

// parseFilter 解析通用过滤条件；时间窗缺失返回错误。
func parseFilter(r *http.Request, limit, offset int) (domain.Filter, error) {
	query := r.URL.Query()
	from, err := parseTime(query.Get("from"))
	if err != nil {
		return domain.Filter{}, errors.New("from must be RFC3339 timestamp")
	}
	to, err := parseTime(query.Get("to"))
	if err != nil {
		return domain.Filter{}, errors.New("to must be RFC3339 timestamp")
	}
	return domain.Filter{
		From:         from,
		To:           to,
		ActorID:      strings.TrimSpace(query.Get("actor_id")),
		Action:       strings.TrimSpace(query.Get("action")),
		Channel:      strings.TrimSpace(query.Get("channel")),
		Outcome:      strings.TrimSpace(query.Get("outcome")),
		MerchantID:   strings.TrimSpace(query.Get("merchant_id")),
		ResourceType: strings.TrimSpace(query.Get("resource_type")),
		ResourceID:   strings.TrimSpace(query.Get("resource_id")),
		TraceID:      strings.TrimSpace(query.Get("trace_id")),
		Limit:        limit,
		Offset:       offset,
	}, nil
}

// parseTime 解析 RFC3339 时间；空值返回零值交由 Filter.Validate 报错。
func parseTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

// toEventDTO 把审计事件转换为响应 DTO。
func toEventDTO(event audit.Event) EventDTO {
	return EventDTO{
		EventID:      event.EventID,
		Action:       event.Action,
		ActorID:      event.ActorID,
		ActorType:    event.ActorType,
		Channel:      event.Channel,
		MerchantID:   event.MerchantID,
		ResourceType: event.ResourceType,
		ResourceID:   event.ResourceID,
		Outcome:      string(event.Outcome),
		OccurredAt:   event.OccurredAt.UTC(),
		TraceID:      event.TraceID,
		Service:      event.Service,
		Reason:       event.Reason,
		Attrs:        event.Attrs,
	}
}

// toExportJobDTO 把任务转换为响应；仅成功且未过期时给出下载路径。
func toExportJobDTO(job domain.ExportJob) ExportJobDTO {
	dto := ExportJobDTO{
		ExportID:  job.ID,
		Status:    string(job.Status),
		Rows:      job.RowCount,
		CreatedAt: job.CreatedAt.UTC(),
		ExpiresAt: job.ExpiresAt.UTC(),
	}
	if !job.FinishedAt.IsZero() {
		finished := job.FinishedAt.UTC()
		dto.FinishedAt = &finished
	}
	if job.Status == domain.ExportFailed {
		dto.Error = job.LastError
	}
	if job.Downloadable(time.Now().UTC()) {
		dto.DownloadPath = "/admin/v1/audit/exports/" + job.ID + "/download"
	}
	return dto
}
