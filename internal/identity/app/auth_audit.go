package app

// 本文件承载认证用例的审计写入与客户端摘要辅助函数（MNT-02）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// emitAudit 写入审计事件；未配置 emitter 时跳过。
func (s *AuthService) emitAudit(
	ctx context.Context,
	action, actorID, merchantID, resourceType, resourceID string,
	outcome audit.Outcome,
	reason, traceID string,
	attrs map[string]string,
) error {
	return s.emitAuditAs(ctx, s.actorType, action, actorID, merchantID, resourceType, resourceID,
		outcome, reason, traceID, attrs)
}

// emitAuditAs 以指定 actor_type 写审计事件；system 用于自动建号等非用户触发操作。
func (s *AuthService) emitAuditAs(
	ctx context.Context,
	actorType, action, actorID, merchantID, resourceType, resourceID string,
	outcome audit.Outcome,
	reason, traceID string,
	attrs map[string]string,
) error {
	if s.audit == nil {
		return nil
	}
	eventID, err := s.ids.NextString()
	if err != nil {
		return apperr.Internal("generate audit event id failed", err)
	}
	event := audit.Event{
		EventID:      eventID,
		Action:       action,
		ActorID:      actorID,
		ActorType:    actorType,
		Channel:      string(s.channel),
		MerchantID:   merchantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Outcome:      outcome,
		OccurredAt:   s.clock.Now().UTC(),
		TraceID:      normalizeTraceID(traceID),
		Service:      s.service,
		Reason:       reason,
		Attrs:        attrs,
	}
	if err := s.audit.Emit(ctx, event); err != nil {
		return apperr.Internal("emit audit event failed", err)
	}
	return nil
}

// emitAccountLocked 仅在本次失败真正触发锁定迁移时补发 auth.account.locked。
//
// 已锁定账号的后续失败由 RecordFailure 返回 lockedNow=false，避免重复审计。
func (s *AuthService) emitAccountLocked(ctx context.Context, account *domain.Account, traceID string) error {
	if account == nil {
		return nil
	}
	if s.metrics != nil {
		s.metrics.ObserveLockout(ctx, s.channel)
	}
	return s.emitAudit(ctx, audit.ActionAuthAccountLocked, account.ID, account.MerchantID, "account", account.ID,
		audit.OutcomeFailure, domain.LoginReasonLocked, traceID, nil)
}

// HashClientRef 返回客户端 IP / UA 的不可逆摘要；空值返回空串。
func HashClientRef(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

// reasonFromAuthError 从守卫错误链中提取审计原因。
func reasonFromAuthError(err error) string {
	switch {
	case errors.Is(err, domain.ErrAccountLocked):
		return domain.LoginReasonLocked
	case errors.Is(err, domain.ErrAccountDisabled):
		return domain.LoginReasonDisabled
	default:
		return domain.LoginReasonBadCredential
	}
}

// loginRateKey 构造登录限流键：channel + 账号摘要 + IP 摘要。
func loginRateKey(channel domain.Channel, accountRef, ipHash string) string {
	if ipHash == "" {
		return string(channel) + ":login:" + accountRef
	}
	return string(channel) + ":login:" + accountRef + ":" + ipHash
}

// shortHash 截断摘要，避免审计 attrs 过长。
func shortHash(value string) string {
	if len(value) <= 16 {
		return value
	}
	return value[:16]
}

// normalizeTraceID 为空 TraceID 提供稳定占位，保证审计事件必填字段成立。
func normalizeTraceID(traceID string) string {
	trimmed := strings.TrimSpace(traceID)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}
