package domain

import (
	"errors"
	"fmt"
)

// 领域错误；应用层据此翻译为 HTTP 语义，仓储实现也复用同一组哨兵。
var (
	// ErrNotFound 通知单不存在。
	ErrNotFound = errors.New("notification: request not found")
	// ErrTenantIDRequired 租户隔离键必填。
	ErrTenantIDRequired = errors.New("notification: tenant id is required")
	// ErrTenantIDTooLong 租户隔离键超长。
	ErrTenantIDTooLong = fmt.Errorf("notification: tenant id must be at most %d characters", MaxActorLength)
	// ErrIDTooLong 通知单 ID 超长。
	ErrIDTooLong = fmt.Errorf("notification: id must be at most %d characters", MaxIDLength)
	// ErrSceneRequired 场景键必填。
	ErrSceneRequired = errors.New("notification: scene is required")
	// ErrSceneTooLong 场景键超长。
	ErrSceneTooLong = fmt.Errorf("notification: scene must be at most %d characters", MaxSceneLength)
	// ErrRecipientRequired 收件人引用必填。
	ErrRecipientRequired = errors.New("notification: recipient ref is required")
	// ErrRecipientTooLong 收件人引用超长。
	ErrRecipientTooLong = fmt.Errorf("notification: recipient ref must be at most %d characters", MaxRecipientRefLength)
	// ErrIdempotencyKeyRequired 幂等键必填。
	ErrIdempotencyKeyRequired = errors.New("notification: idempotency key is required")
	// ErrIdempotencyKeyTooLong 幂等键超长。
	ErrIdempotencyKeyTooLong = fmt.Errorf("notification: idempotency key must be at most %d characters", MaxIdempotencyKeyLength)
	// ErrChannelRequired 期望渠道至少一个。
	ErrChannelRequired = errors.New("notification: at least one channel is required")
	// ErrTemplateKeyRequired 模板键必填。
	ErrTemplateKeyRequired = errors.New("notification: template key is required")
	// ErrTemplateKeyTooLong 模板键超长。
	ErrTemplateKeyTooLong = fmt.Errorf("notification: template key must be at most %d characters", MaxTemplateKeyLength)
	// ErrLocaleTooLong 语言标签超长。
	ErrLocaleTooLong = fmt.Errorf("notification: locale must be at most %d characters", MaxLocaleLength)
	// ErrDuplicateRequest 同幂等键的通知已存在（重复事件 / 重放，幂等吸收）。
	ErrDuplicateRequest = errors.New("notification: duplicate request for idempotency key")
	// ErrRequestTerminal 通知单已终态，不可再迁移。
	ErrRequestTerminal = errors.New("notification: request is terminal")
	// ErrRejectReasonRequired 拒发缺少原因。
	ErrRejectReasonRequired = errors.New("notification: reject reason is required")
	// ErrRejectReasonTooLong 拒发原因超长。
	ErrRejectReasonTooLong = fmt.Errorf("notification: reject reason must be at most %d characters", MaxRejectReasonLength)
	// ErrVersionConflict 通知单并发状态更新冲突（乐观锁失败）。
	ErrVersionConflict = errors.New("notification: concurrent request update detected")
	// ErrDeliveryNotFound 投递明细不存在。
	ErrDeliveryNotFound = errors.New("notification: delivery not found")
	// ErrDeliveryTerminal 投递明细已终态，不可再迁移。
	ErrDeliveryTerminal = errors.New("notification: delivery is terminal")
	// ErrSemanticCodeTooLong 失败语义码超长。
	ErrSemanticCodeTooLong = fmt.Errorf("notification: semantic code must be at most %d characters", MaxSemanticCodeLength)
	// ErrProviderCodeTooLong 供应商原始码超长。
	ErrProviderCodeTooLong = fmt.Errorf("notification: provider code must be at most %d characters", MaxProviderCodeLength)
	// ErrProviderRequired 供应商标识必填。
	ErrProviderRequired = errors.New("notification: provider is required")
	// ErrProviderTooLong 供应商标识超长。
	ErrProviderTooLong = fmt.Errorf("notification: provider must be at most %d characters", MaxProviderLength)
	// ErrTemplateNotFound 渲染所需模板不存在。
	ErrTemplateNotFound = errors.New("notification: template not found")
	// ErrTemplateDisabled 模板已停用，不可渲染。
	ErrTemplateDisabled = errors.New("notification: template is disabled")
	// ErrTemplateSubjectRequired 邮件模板主题必填。
	ErrTemplateSubjectRequired = errors.New("notification: email template subject is required")
	// ErrTemplateSubjectTooLong 邮件模板主题超长。
	ErrTemplateSubjectTooLong = fmt.Errorf("notification: template subject must be at most %d characters", MaxTemplateSubjectLength)
	// ErrTemplateBodyRequired 模板正文必填。
	ErrTemplateBodyRequired = errors.New("notification: template body is required")
	// ErrTemplateBodyTooLong 模板正文超长。
	ErrTemplateBodyTooLong = fmt.Errorf("notification: template body must be at most %d characters", MaxTemplateBodyLength)
	// ErrTemplateInvalidPlaceholder 模板占位符语法非法。
	ErrTemplateInvalidPlaceholder = errors.New("notification: template contains invalid placeholder")
	// ErrSubjectIDRequired 偏好主体 ID 必填。
	ErrSubjectIDRequired = errors.New("notification: preference subject id is required")
	// ErrTransactionalOptOut 事务类场景不允许退订（账号安全与合同动作必须可达）。
	ErrTransactionalOptOut = errors.New("notification: transactional scene cannot be opted out")
	// ErrTargetHMACInvalid 黑名单目标摘要非法（必须 64 位十六进制 SHA/HMAC）。
	ErrTargetHMACInvalid = errors.New("notification: blacklist target hmac must be 64 hex chars")
	// ErrBlacklistReasonTooLong 黑名单原因超长。
	ErrBlacklistReasonTooLong = fmt.Errorf("notification: blacklist reason must be at most %d characters", MaxBlacklistReasonLength)
	// ErrBlacklisted 目标在黑名单中，拒绝投递。
	ErrBlacklisted = errors.New("notification: recipient is blacklisted")
	// ErrExpired 通知已过期，弃发。
	ErrExpired = errors.New("notification: request expired")
	// ErrNoRoute 场景没有可用渠道路由。
	ErrNoRoute = errors.New("notification: no route for scene and channel")
	// ErrRateLimited 触发频控（供应商保护或收件人限额）；统一 42900 语义。
	ErrRateLimited = errors.New("notification: rate limited")
	// ErrChannelUnavailable 渠道整体不可用（熔断 Open），按可重试处理。
	ErrChannelUnavailable = errors.New("notification: channel unavailable")
)

// InvalidSceneError 表示场景键含非法字符。
type InvalidSceneError struct {
	// Scene 非法场景键。
	Scene string
}

// Error 实现 error。
func (e *InvalidSceneError) Error() string {
	return fmt.Sprintf("notification: invalid scene %q (allowed: a-z 0-9 . _ -)", e.Scene)
}

// InvalidTemplateKeyError 表示模板键含非法字符。
type InvalidTemplateKeyError struct {
	// Key 非法模板键。
	Key string
}

// Error 实现 error。
func (e *InvalidTemplateKeyError) Error() string {
	return fmt.Sprintf("notification: invalid template key %q (allowed: a-z 0-9 . _ -)", e.Key)
}

// InvalidChannelError 表示未定义的渠道。
type InvalidChannelError struct {
	// Channel 非法渠道取值。
	Channel string
}

// Error 实现 error。
func (e *InvalidChannelError) Error() string {
	return fmt.Sprintf("notification: unsupported channel %q", e.Channel)
}

// ChannelNotImplementedError 表示契约预留但尚无投递实现的渠道（如 inbox）。
type ChannelNotImplementedError struct {
	// Channel 渠道。
	Channel Channel
}

// Error 实现 error。
func (e *ChannelNotImplementedError) Error() string {
	return fmt.Sprintf("notification: channel %q is reserved but not implemented", e.Channel)
}

// InvalidPriorityError 表示未定义的优先级。
type InvalidPriorityError struct {
	// Priority 非法取值。
	Priority string
}

// Error 实现 error。
func (e *InvalidPriorityError) Error() string {
	return fmt.Sprintf("notification: unsupported priority %q", e.Priority)
}

// InvalidLocaleError 表示语言标签含非法字符。
type InvalidLocaleError struct {
	// Locale 非法取值。
	Locale string
}

// Error 实现 error。
func (e *InvalidLocaleError) Error() string {
	return fmt.Sprintf("notification: invalid locale %q", e.Locale)
}

// InvalidTransitionError 表示通知单状态机不允许的迁移。
type InvalidTransitionError struct {
	// From 当前状态。
	From RequestStatus
	// To 目标状态。
	To RequestStatus
}

// Error 实现 error。
func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("notification: cannot transition request from %s to %s", e.From, e.To)
}

// InvalidDeliveryTransitionError 表示投递状态机不允许的迁移。
type InvalidDeliveryTransitionError struct {
	// From 当前状态。
	From DeliveryStatus
	// To 目标状态。
	To DeliveryStatus
}

// Error 实现 error。
func (e *InvalidDeliveryTransitionError) Error() string {
	return fmt.Sprintf("notification: cannot transition delivery from %s to %s", e.From, e.To)
}

// InvalidSubjectTypeError 表示未定义的偏好主体类型。
type InvalidSubjectTypeError struct {
	// Value 非法取值。
	Value string
}

// Error 实现 error。
func (e *InvalidSubjectTypeError) Error() string {
	return fmt.Sprintf("notification: unsupported subject type %q", e.Value)
}

// InvalidSceneClassError 表示未定义的场景分类。
type InvalidSceneClassError struct {
	// Value 非法取值。
	Value string
}

// Error 实现 error。
func (e *InvalidSceneClassError) Error() string {
	return fmt.Sprintf("notification: unsupported scene class %q", e.Value)
}

// InvalidBlacklistSourceError 表示未定义的黑名单来源。
type InvalidBlacklistSourceError struct {
	// Value 非法取值。
	Value string
}

// Error 实现 error。
func (e *InvalidBlacklistSourceError) Error() string {
	return fmt.Sprintf("notification: unsupported blacklist source %q", e.Value)
}

// TemplateParamMissingError 表示模板变量缺失或取值为空。
type TemplateParamMissingError struct {
	// Key 变量名。
	Key string
}

// Error 实现 error。
func (e *TemplateParamMissingError) Error() string {
	return fmt.Sprintf("notification: template param %q is missing or empty", e.Key)
}

// TemplateParamUnknownError 表示传入变量不在模板白名单中。
type TemplateParamUnknownError struct {
	// Key 变量名。
	Key string
}

// Error 实现 error。
func (e *TemplateParamUnknownError) Error() string {
	return fmt.Sprintf("notification: template param %q is not declared by template", e.Key)
}

// TemplateParamTooLongError 表示变量取值超长。
type TemplateParamTooLongError struct {
	// Key 变量名。
	Key string
}

// Error 实现 error。
func (e *TemplateParamTooLongError) Error() string {
	return fmt.Sprintf("notification: template param %q exceeds %d characters", e.Key, MaxTemplateParamValueLength)
}
