package app

import (
	"context"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// ActionSend 是 A 端代发审计动作（EVENTS.md notification.* 段）。
const ActionSend = "notification.send"

// ResourceTypeRequest 代发审计的资源类型。
// （与 replay.go 的 ResourceTypeRequest 同名复用。）

// SendService 是 A 端代发用例（ENT-10 NT-06）：运营经管理端显式触发一次
// 通知。走同一 Outbox 出口（铁律 5.1.3），代发动作入审计。
type SendService struct {
	dispatcher *OutboxDispatcher
	audit      auditContext
	ids        id.Generator
	now        func() time.Time
	defaults   Options
}

// NewSendService 构造代发用例；出口 / ID 生成器 / 审计缺一不可。
func NewSendService(dispatcher *OutboxDispatcher, ids id.Generator,
	emitter audit.Emitter, opts Options) (*SendService, error) {
	switch {
	case dispatcher == nil:
		return nil, apperr.Internal("notification send service requires a dispatcher", nil)
	case ids == nil:
		return nil, apperr.Internal("notification send service requires an id generator", nil)
	case emitter == nil:
		return nil, apperr.Internal("notification send service requires an audit emitter", nil)
	}
	now := time.Now
	return &SendService{
		dispatcher: dispatcher,
		audit:      auditContext{emitter: emitter, ids: ids, service: opts.Service, now: now, observer: observerOrNoop(opts.Observer)},
		ids:        ids,
		now:        now,
		defaults:   opts,
	}, nil
}

// SendInput 是代发入参。
type SendInput struct {
	// TenantID 目标租户；平台级传 domain.PlatformTenantID。
	TenantID string
	// Scene 业务场景键。
	Scene string
	// SceneClass 场景分类（空取 operational）。
	SceneClass string
	// Recipient 收件人寻址（渠道地址；明文仅入事件载荷）。
	Recipient Recipient
	// Channels 期望渠道。
	Channels []string
	// TemplateKey 模板键。
	TemplateKey string
	// Locale 渲染语言；空取默认。
	Locale string
	// Params 渲染参数。
	Params map[string]string
	// Priority 优先级；空取 normal。
	Priority string
	// Reference 业务引用（低基数非 PII）。
	Reference string
	// Actor 操作者。
	Actor Actor
}

// Send 代发一条通知；返回通知单 ID。
//
// 与业务域接入的差异只在触发源：载荷结构、出口、消费编排完全一致；
// 幂等键由服务端生成（代发不支持调用方自定键，防止滥用绕过幂等）。
func (s *SendService) Send(ctx context.Context, in SendInput) (string, error) {
	if strings.TrimSpace(in.TenantID) == "" {
		return "", apperr.BadRequest("tenant id is required")
	}
	if strings.TrimSpace(in.Scene) == "" {
		return "", apperr.BadRequest("scene is required")
	}
	if len(in.Channels) == 0 {
		return "", apperr.BadRequest("at least one channel is required")
	}
	if strings.TrimSpace(in.TemplateKey) == "" {
		return "", apperr.BadRequest("template key is required")
	}
	requestID, err := s.ids.NextString()
	if err != nil {
		return "", apperr.Internal("generate notification id failed", err)
	}
	priority := strings.TrimSpace(in.Priority)
	if priority == "" {
		priority = string(domain.PriorityNormal)
	}
	sceneClass := strings.TrimSpace(in.SceneClass)
	if sceneClass == "" {
		sceneClass = string(domain.ClassOperational)
	}
	ttl := s.defaults.DefaultNotificationTTL
	if ttl <= 0 {
		ttl = domain.DefaultNotificationTTL
	}
	payload := RequestPayload{
		SchemaVersion:  SchemaVersionV1,
		RequestID:      requestID,
		TenantID:       in.TenantID,
		Scene:          in.Scene,
		SceneClass:     sceneClass,
		RecipientRef:   recipientDigest(in.Recipient),
		Recipient:      in.Recipient,
		Channels:       in.Channels,
		TemplateKey:    in.TemplateKey,
		Locale:         in.Locale,
		Params:         in.Params,
		IdempotencyKey: "notify:admin.send:" + requestID,
		Priority:       priority,
		Actor:          actorRef(in.Actor),
		Reference:      in.Reference,
		ExpiresAt:      s.now().Add(ttl),
	}
	if err := s.dispatcher.Dispatch(ctx, payload); err != nil {
		return "", err
	}
	attrs := map[string]string{
		"scene":         in.Scene,
		"channels":      strings.Join(in.Channels, ","),
		"template_key":  in.TemplateKey,
		"recipient_ref": payload.RecipientRef,
	}
	if err := s.audit.emit(ctx, in.Actor, ActionSend, ResourceTypeRequest, requestID,
		audit.OutcomeSuccess, "", attrs); err != nil {
		return "", apperr.Internal("emit send audit failed", err)
	}
	return requestID, nil
}

// recipientDigest 收件人引用（代发场景无 subject_id 时用摘要）。
func recipientDigest(r Recipient) string {
	target := r.Email
	if target == "" {
		target = r.Phone
	}
	if target == "" {
		target = r.PushToken
	}
	if target == "" {
		target = r.UserID
	}
	return hashTarget(target)
}

func actorRef(a Actor) string {
	if ref := strings.TrimSpace(a.SubjectID); ref != "" {
		return ref
	}
	return "admin.send"
}
