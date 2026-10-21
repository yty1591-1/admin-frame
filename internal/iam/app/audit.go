package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/contextx"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// 审计资源类型（AUD-04 resource_type）。
const (
	ResourceTypeRole    = "iam_role"
	ResourceTypeBinding = "iam_binding"
)

// 审计属性键，保持稳定便于 SIEM 检索。
const (
	attrOperation   = "operation"
	attrRoleCode    = "role_code"
	attrRoleID      = "role_id"
	attrSubjectType = "subject_type"
	attrSubjectID   = "subject_id"
	attrAdded       = "added"
	attrRemoved     = "removed"
	attrExpiresAt   = "expires_at"
)

// AttrOperation 是审计属性中「变更类型」的键（create / update / disable / bind …）。
//
// 观测层与 SIEM 都依赖该键统计变更量，因此导出为稳定契约；改名等同破坏性变更。
const AttrOperation = attrOperation

// ChangeRecorder 是 IAM 变更的审计出口。
//
// 生产实现应把事件写入 iam_outbox 并与业务写入同事务（docs/rbac-iam.md D9）；
// local / demo 可用 SlogRecorder 直接落结构化日志。Record 返回错误时调用方必须
// 让业务写入失败（fail-closed），不得只记日志后继续。
type ChangeRecorder interface {
	Record(ctx context.Context, event audit.Event) error
}

// SlogRecorder 使用 audit.Emitter 同步写审计日志（仅 local / demo）。
type SlogRecorder struct {
	Emitter audit.Emitter
}

// Record 校验并写出审计事件。
func (r SlogRecorder) Record(ctx context.Context, event audit.Event) error {
	if r.Emitter == nil {
		return apperr.Internal("audit emitter is not configured", nil)
	}
	if err := r.Emitter.Emit(ctx, event); err != nil {
		return apperr.Internal("write audit event", err)
	}
	return nil
}

// NopRecorder 丢弃全部事件；仅用于测试，生产禁止装配。
type NopRecorder struct {
	// Log 非空时记录一条 Warn，提醒当前处于未审计状态。
	Log *slog.Logger
}

// Record 丢弃事件（可观测上留痕）。
func (r NopRecorder) Record(ctx context.Context, event audit.Event) error {
	if r.Log != nil {
		r.Log.WarnContext(ctx, "iam change is not audited", "action", event.Action, "resource_id", event.ResourceID)
	}
	return nil
}

// AuditMeta 是进程级的审计身份字段。
type AuditMeta struct {
	// Service 进程名（配置 service），写入 audit.Event.Service。
	Service string
	// ActorType 操作人类型；A 端为 staff，B 端为 merchant_staff。
	ActorType string
}

// auditBuilder 统一组装审计事件，避免各用例遗漏必填字段（AUD-04）。
type auditBuilder struct {
	ids  id.Generator
	meta AuditMeta
}

// newAuditBuilder 构造审计事件工厂；ids 为 nil 时使用雪花（单实例/测试）。
func newAuditBuilder(ids id.Generator, meta AuditMeta) auditBuilder {
	if ids == nil {
		ids = id.MustSnowflake(id.Options{NodeID: 1})
	}
	if meta.Service == "" {
		meta.Service = "admin-frame"
	}
	return auditBuilder{ids: ids, meta: meta}
}

// build 组装并校验审计事件；EventID 生成失败返回内部错误。
func (b auditBuilder) build(ctx context.Context, p auditParams) (audit.Event, error) {
	eventID, err := b.ids.NextString()
	if err != nil {
		return audit.Event{}, apperr.Internal("generate audit event id", err)
	}
	traceID := strings.TrimSpace(contextx.RequestID(ctx))
	if traceID == "" {
		// 非 HTTP 入口（worker / 定时任务）没有 request id，用事件 ID 兜底，
		// 保证审计必填字段完整且仍可精确检索单条事件。
		traceID = eventID
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       p.action,
		ActorID:      p.actor,
		ActorType:    b.meta.ActorType,
		Channel:      p.channel,
		MerchantID:   p.merchantID,
		ResourceType: p.resourceType,
		ResourceID:   p.resourceID,
		Outcome:      p.outcome,
		OccurredAt:   time.Now().UTC(),
		TraceID:      traceID,
		Service:      b.meta.Service,
		Reason:       p.reason,
		Attrs:        p.attrs,
	}
	if err := event.Validate(); err != nil {
		return audit.Event{}, apperr.Internal("invalid audit event", err)
	}
	return event, nil
}

// auditParams 是 build 的入参。
type auditParams struct {
	action       string
	actor        string
	channel      string
	merchantID   string
	resourceType string
	resourceID   string
	outcome      audit.Outcome
	reason       string
	attrs        map[string]string
}

// record 组装并写出审计事件；写失败转为 50000（fail-closed）。
func (b auditBuilder) record(ctx context.Context, recorder ChangeRecorder, p auditParams) error {
	if recorder == nil {
		return apperr.Internal("audit recorder is not configured", nil)
	}
	event, err := b.build(ctx, p)
	if err != nil {
		return err
	}
	if err := recorder.Record(ctx, event); err != nil {
		return apperr.Internal("record iam change audit", err)
	}
	return nil
}

// codesAttr 把权限码切片编码为逗号分隔字符串，空集合输出空串。
func codesAttr(codes []string) string {
	return strings.Join(codes, ",")
}
