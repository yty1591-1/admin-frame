package app

import (
	"context"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// emitAccessEvent 记录对审计数据的访问事件（查询 / 导出 / 下载）。
//
// 同步导出与异步导出共用同一套字段补全规则：actor、channel、trace_id 三者
// 任一缺失都会让事件无法追责，因此在这里统一兜底，而不是让两个调用点各写一遍。
func emitAccessEvent(
	ctx context.Context,
	emitter audit.Emitter,
	ids id.Generator,
	service string,
	action, resourceID string,
	meta AccessMeta,
	outcome audit.Outcome,
	attrs map[string]string,
) error {
	if emitter == nil {
		return nil
	}
	if ids == nil {
		return nil
	}
	eventID, err := ids.NextString()
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
		Outcome:      outcome,
		OccurredAt:   time.Now().UTC(),
		TraceID:      traceID,
		Service:      service,
		Attrs:        attrs,
	}
	return emitter.Emit(ctx, event)
}
