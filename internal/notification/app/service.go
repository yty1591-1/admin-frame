// Package app 实现通知域的应用用例（ENT-10）：模板、偏好、黑名单、
// 投递编排与状态查询。
//
// 与租户域（internal/tenant/app）同款范式：领域错误在此统一翻译为
// apperr 语义；审计动作常量以 `notification.*` 命名并与
// docs/audit/EVENTS.md 的 notification 段对账（check-notification 门禁）。
package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
	"github.com/admin-frame/admin-frame/internal/notification/domain"
	"github.com/admin-frame/admin-frame/pkg/id"
)

// Observer 是通知域的可观测端口：编排埋点与审计失败计数经此上报，
// 由 observability 包实现；nil 安全由构造保证（noop）。
type Observer interface {
	// AuditEmitFailed 审计事件写出失败计数（Outbox 模式下非零即应告警）。
	AuditEmitFailed()
}

// noopObserver 是 Observer 的空实现（单测 / 内存模式）。
type noopObserver struct{}

func (noopObserver) AuditEmitFailed() {}

func observerOrNoop(o Observer) Observer {
	if o == nil {
		return noopObserver{}
	}
	return o
}

// Options 是用例参数。
type Options struct {
	// Service 服务名，写入审计事件。
	Service string
	// Observer 可观测端口；nil 时使用 noop 实现。
	Observer Observer
	// DefaultLocale 渲染默认语言标签；空取 "zh-CN"。
	DefaultLocale string
	// DefaultNotificationTTL 通知默认时效；<=0 取领域默认值（24h）。
	DefaultNotificationTTL time.Duration
}

// Actor 是操作者信息，来自 Principal 与请求上下文。
type Actor struct {
	// SubjectID 操作者主体 ID；系统触发取系统标识（如 "system"）。
	SubjectID string
	// ActorType 操作者类型（user / service / system）。
	ActorType string
	// Channel 端别（admin / biz / system）。
	Channel string
	// MerchantID B 端商户；A 端为空。
	MerchantID string
	// TraceID 请求追踪 ID。
	TraceID string
}

// auditContext 封装审计写入依赖，供通知域各用例复用
// （对齐租户域 auditContext；差异仅在事件 ID 兜底前缀）。
type auditContext struct {
	emitter  audit.Emitter
	ids      id.Generator
	service  string
	now      func() time.Time
	observer Observer
}

func (a auditContext) emit(ctx context.Context, actor Actor, action, resourceType, resourceID string,
	outcome audit.Outcome, reason string, attrs map[string]string) error {
	eventID := ""
	if a.ids != nil {
		if generated, err := a.ids.NextString(); err == nil {
			eventID = generated
		}
	}
	if eventID == "" {
		// 生成失败也必须有事件 ID，否则整条审计会被 Validate 拒绝而静默丢失。
		eventID = "notification-" + a.now().UTC().Format("20060102150405.000000000")
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actor.SubjectID,
		ActorType:    actor.ActorType,
		Channel:      defaultChannel(actor.Channel),
		MerchantID:   actor.MerchantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Outcome:      outcome,
		OccurredAt:   a.now().UTC(),
		TraceID:      actor.TraceID,
		Service:      a.service,
		Reason:       truncate(reason, 255),
		Attrs:        attrs,
	}
	if err := a.emitter.Emit(ctx, event); err != nil {
		a.observer.AuditEmitFailed()
		return err
	}
	return nil
}

func defaultChannel(channel string) string {
	switch strings.TrimSpace(channel) {
	case "admin", "biz", "system":
		return strings.TrimSpace(channel)
	default:
		return "internal"
	}
}

func truncate(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return strings.TrimSpace(s)
	}
	return string(runes[:max])
}

// translate 把领域错误翻译为 apperr 语义；已翻译的 apperr 原样透传。
func translate(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		return err
	}
	var invalidTransition *domain.InvalidTransitionError
	var invalidDeliveryTransition *domain.InvalidDeliveryTransitionError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.NotFound("notification request not found")
	case errors.Is(err, domain.ErrDeliveryNotFound):
		return apperr.NotFound("notification delivery not found")
	case errors.Is(err, domain.ErrTemplateNotFound):
		return apperr.NotFound("notification template not found")
	case errors.Is(err, domain.ErrDuplicateRequest):
		// 幂等键冲突是正常竞态（重复事件 / 双副本并发），对调用方
		// 语义是「已受理」，以 409 表达并保留哨兵链。
		return apperr.New(apperr.CodeConflict, "duplicate notification request", err)
	case errors.Is(err, domain.ErrRequestTerminal),
		errors.Is(err, domain.ErrDeliveryTerminal):
		return apperr.Conflict(err.Error())
	case errors.As(err, &invalidTransition),
		errors.As(err, &invalidDeliveryTransition):
		return apperr.Conflict(err.Error())
	case errors.Is(err, domain.ErrVersionConflict):
		return apperr.Conflict("notification request changed concurrently, retry")
	case errors.Is(err, domain.ErrBlacklisted):
		return apperr.New(apperr.CodeConflict, "recipient is blacklisted", err)
	case errors.Is(err, domain.ErrExpired):
		return apperr.Conflict("notification request expired")
	case errors.Is(err, domain.ErrRateLimited):
		// 统一 42900 语义（规划 5.1 约束 6）：配额与频控同一码。
		return apperr.TooManyRequests("notification rate limited")
	case errors.Is(err, domain.ErrChannelUnavailable):
		return apperr.Unavailable("notification channel temporarily unavailable")
	case errors.Is(err, domain.ErrNoRoute):
		return apperr.NotFound("no route configured for scene and channel")
	case isInvalidEnum(err):
		return apperr.BadRequest(err.Error())
	case isInvalidTemplateParam(err):
		return apperr.BadRequest(err.Error())
	default:
		return apperr.BadRequest(err.Error())
	}
}

// translateIfNeeded 供用例出口统一调用：apperr 透传，其余翻译。
func translateIfNeeded(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperr.AppError
	if errors.As(err, &appErr) {
		return err
	}
	return translate(err)
}

// isInvalidEnum 汇总「枚举 / 取值非法」类错误 → 400。
func isInvalidEnum(err error) bool {
	var scene *domain.InvalidSceneError
	var tplKey *domain.InvalidTemplateKeyError
	var channel *domain.InvalidChannelError
	var notImpl *domain.ChannelNotImplementedError
	var priority *domain.InvalidPriorityError
	var locale *domain.InvalidLocaleError
	var subjectType *domain.InvalidSubjectTypeError
	var sceneClass *domain.InvalidSceneClassError
	var blacklistSource *domain.InvalidBlacklistSourceError
	switch {
	case errors.As(err, &scene),
		errors.As(err, &tplKey),
		errors.As(err, &channel),
		errors.As(err, &notImpl),
		errors.As(err, &priority),
		errors.As(err, &locale),
		errors.As(err, &subjectType),
		errors.As(err, &sceneClass),
		errors.As(err, &blacklistSource),
		errors.Is(err, domain.ErrTransactionalOptOut),
		errors.Is(err, domain.ErrTargetHMACInvalid):
		return true
	default:
		return false
	}
}

// isInvalidTemplateParam 汇总模板渲染参数错误 → 400。
func isInvalidTemplateParam(err error) bool {
	var missing *domain.TemplateParamMissingError
	var unknown *domain.TemplateParamUnknownError
	var tooLong *domain.TemplateParamTooLongError
	switch {
	case errors.As(err, &missing),
		errors.As(err, &unknown),
		errors.As(err, &tooLong):
		return true
	default:
		return false
	}
}
