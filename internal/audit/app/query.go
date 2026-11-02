// Package app 编排审计查询、导出与到期清理用例（ENT-04）。
package app

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/audit/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// QueryOptions 是查询与导出的限制参数，来自 audit.query / audit.export 配置。
type QueryOptions struct {
	// MaxRange 单次查询允许的最大时间跨度。
	MaxRange time.Duration
	// MaxPageSize 每页条数上限。
	MaxPageSize int
	// ExportMaxRange 单次导出允许的最大时间跨度。
	ExportMaxRange time.Duration
	// ExportMaxRows 单次导出的最大行数。
	ExportMaxRows int
}

// AccessMeta 是访问审计数据的操作者信息，用于记录查询与导出行为。
type AccessMeta struct {
	// ActorID 操作者主体 ID。
	ActorID string
	// ActorType 操作者类型。
	ActorType string
	// Channel 端别。
	Channel string
	// MerchantID B 端商户 ID。
	MerchantID string
	// TraceID 请求追踪 ID。
	TraceID string
}

// QueryService 提供审计事件查询与导出用例。
type QueryService struct {
	reader  domain.Reader
	emitter audit.Emitter
	ids     id.Generator
	opts    QueryOptions
	service string
}

// NewQueryService 构造审计查询服务；reader 与 ids 必填。
func NewQueryService(reader domain.Reader, emitter audit.Emitter, ids id.Generator, opts QueryOptions, service string) (*QueryService, error) {
	if reader == nil {
		return nil, apperr.Internal("audit query service requires a reader", nil)
	}
	if ids == nil {
		return nil, apperr.Internal("audit query service requires an id generator", nil)
	}
	if opts.MaxPageSize <= 0 {
		opts.MaxPageSize = 100
	}
	return &QueryService{
		reader:  reader,
		emitter: emitter,
		ids:     ids,
		opts:    opts,
		service: service,
	}, nil
}

// ListResult 是分页查询结果。
type ListResult struct {
	// Items 当前页事件。
	Items []audit.Event
	// Total 满足条件的总条数。
	Total int
}

// List 按条件分页查询审计事件，并记录 audit.query 事件。
//
// 访问审计轨迹本身也必须留痕：谁在什么时间窗、用什么条件查了审计，
// 否则「可追责」只对业务成立、对审计系统自身不成立。
func (s *QueryService) List(ctx context.Context, filter domain.Filter, meta AccessMeta) (ListResult, error) {
	if err := filter.Validate(s.opts.MaxRange); err != nil {
		return ListResult{}, apperr.BadRequest(err.Error())
	}
	if filter.Limit > s.opts.MaxPageSize {
		filter.Limit = s.opts.MaxPageSize
	}
	items, err := s.reader.List(ctx, filter)
	if err != nil {
		return ListResult{}, apperr.Internal("list audit events", err)
	}
	total, err := s.reader.Count(ctx, filter)
	if err != nil {
		return ListResult{}, apperr.Internal("count audit events", err)
	}
	if emitErr := s.emitAccess(ctx, audit.ActionAuditQuery, exportRangeID(filter), meta, map[string]string{
		"rows":    strconv.Itoa(len(items)),
		"total":   strconv.Itoa(total),
		"from":    filter.From.UTC().Format(time.RFC3339),
		"to":      filter.To.UTC().Format(time.RFC3339),
		"filters": describeFilters(filter),
	}); emitErr != nil {
		return ListResult{}, apperr.Internal("emit audit query event", emitErr)
	}
	return ListResult{Items: items, Total: total}, nil
}

// ExportResult 是导出结果摘要。
type ExportResult struct {
	// Rows 实际导出行数。
	Rows int
	// From 导出起始时间。
	From time.Time
	// To 导出结束时间。
	To time.Time
}

// Export 把审计事件以 CSV 流式写入 w，并记录 data.export 审计事件。
//
// 导出范围与行数双重受限：超限直接拒绝而不是静默截断，避免使用者误以为
// 拿到了全量数据（AUDIT_STANDARDS §5：记录范围摘要，禁止整包 PII）。
func (s *QueryService) Export(ctx context.Context, filter domain.Filter, meta AccessMeta, w io.Writer) (ExportResult, error) {
	if w == nil {
		return ExportResult{}, apperr.Internal("audit export requires a writer", nil)
	}
	filter.Limit = s.opts.ExportMaxRows
	if filter.Offset != 0 {
		return ExportResult{}, apperr.BadRequest("export does not support offset")
	}
	if err := filter.Validate(s.opts.ExportMaxRange); err != nil {
		return ExportResult{}, apperr.BadRequest(err.Error())
	}
	if strings.TrimSpace(meta.ActorID) == "" || strings.TrimSpace(meta.Channel) == "" {
		return ExportResult{}, apperr.BadRequest("export actor and channel are required")
	}
	if s.opts.ExportMaxRows <= 0 {
		return ExportResult{}, apperr.Internal("audit export max rows is not configured", nil)
	}

	writer := csv.NewWriter(w)
	if err := writer.Write(exportHeader); err != nil {
		return ExportResult{}, apperr.Internal("write audit export header", err)
	}
	rows := 0
	streamErr := s.reader.Stream(ctx, filter, func(event audit.Event) error {
		if rows >= s.opts.ExportMaxRows {
			return apperr.BadRequest(fmt.Sprintf(
				"export exceeds maximum of %d rows in the selected range", s.opts.ExportMaxRows))
		}
		if err := writer.Write(exportRow(event)); err != nil {
			return apperr.Internal("write audit export row", err)
		}
		rows++
		return nil
	})
	if streamErr != nil {
		return ExportResult{}, streamErr
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportResult{}, apperr.Internal("flush audit export", err)
	}

	result := ExportResult{Rows: rows, From: filter.From, To: filter.To}
	if err := s.emitAccess(ctx, audit.ActionDataExport, exportRangeID(filter), meta, map[string]string{
		"rows":    strconv.Itoa(rows),
		"from":    filter.From.UTC().Format(time.RFC3339),
		"to":      filter.To.UTC().Format(time.RFC3339),
		"filters": describeFilters(filter),
	}); err != nil {
		// 导出内容已写入响应流，无法回滚；返回错误由上层记录并告警，
		// 不静默吞掉（AUD-03：关键路径必须留痕）。
		return result, apperr.Internal("emit audit export event", err)
	}
	return result, nil
}

// exportHeader 是导出 CSV 的列顺序，与 exportRow 一一对应。
var exportHeader = []string{
	"event_id", "occurred_at", "action", "outcome", "channel",
	"actor_id", "actor_type", "merchant_id",
	"resource_type", "resource_id", "trace_id", "service", "reason",
}

// exportRow 把事件转换为 CSV 行；attrs 不导出，避免二次扩散敏感字段。
func exportRow(event audit.Event) []string {
	return []string{
		event.EventID,
		event.OccurredAt.UTC().Format(time.RFC3339Nano),
		event.Action,
		string(event.Outcome),
		event.Channel,
		event.ActorID,
		event.ActorType,
		event.MerchantID,
		event.ResourceType,
		event.ResourceID,
		event.TraceID,
		event.Service,
		event.Reason,
	}
}

// emitAccess 记录对审计数据的访问（查询 / 导出）。
func (s *QueryService) emitAccess(
	ctx context.Context,
	action, resourceID string,
	meta AccessMeta,
	attrs map[string]string,
) error {
	if s.emitter == nil {
		return nil
	}
	eventID, err := s.ids.NextString()
	if err != nil {
		return err
	}
	// 非 HTTP 入口（或中间件缺失）时用请求 ID、再退化为事件 ID，
	// 保证 AUD-04 的 trace_id 必填且单条事件仍可精确检索。
	traceID := strings.TrimSpace(meta.TraceID)
	if traceID == "" {
		traceID = strings.TrimSpace(contextx.RequestID(ctx))
	}
	if traceID == "" {
		traceID = eventID
	}
	actorID := strings.TrimSpace(meta.ActorID)
	if actorID == "" {
		actorID = "system"
	}
	channel := strings.TrimSpace(meta.Channel)
	if channel == "" {
		channel = "admin"
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actorID,
		ActorType:    meta.ActorType,
		Channel:      channel,
		MerchantID:   meta.MerchantID,
		ResourceType: "audit_event",
		ResourceID:   resourceID,
		Outcome:      audit.OutcomeSuccess,
		OccurredAt:   time.Now().UTC(),
		TraceID:      traceID,
		Service:      s.service,
		Attrs:        attrs,
	}
	return s.emitter.Emit(ctx, event)
}

// exportRangeID 用时间窗构造资源标识，避免资源 ID 里出现过滤原文。
func exportRangeID(filter domain.Filter) string {
	return filter.From.UTC().Format("20060102T150405Z") + "-" + filter.To.UTC().Format("20060102T150405Z")
}

// describeFilters 汇总过滤条件名称，仅用于判断访问范围，不含结果数据。
func describeFilters(filter domain.Filter) string {
	names := make([]string, 0, 8)
	for _, item := range []struct {
		name  string
		value string
	}{
		{"actor_id", filter.ActorID},
		{"action", filter.Action},
		{"channel", filter.Channel},
		{"outcome", filter.Outcome},
		{"merchant_id", filter.MerchantID},
		{"resource_type", filter.ResourceType},
		{"resource_id", filter.ResourceID},
		{"trace_id", filter.TraceID},
	} {
		if strings.TrimSpace(item.value) != "" {
			names = append(names, item.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}
