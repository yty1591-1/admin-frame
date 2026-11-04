package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/audit"
)

// 本文件承载租户切换用例（ENT-09 T-22，D-04=①）。

// SwitchTenantInput 是切换当前租户的入参。
type SwitchTenantInput struct {
	SessionActor
	// TenantID 目标租户（须为该主体的在册成员租户且处于服务期）。
	TenantID string
	// TraceID 请求追踪 ID。
	TraceID string
}

// TenantSwitchResult 是切换结果：同会话重签的访问令牌。
type TenantSwitchResult struct {
	// AccessToken 携带新 current_tenant 声明的访问令牌。
	AccessToken string
	// ExpiresIn 访问令牌有效期（秒）。
	ExpiresIn int64
	// CurrentTenant 切换后的生效租户。
	CurrentTenant string
}

// SwitchTenant 切换 B 端主体的当前生效租户。
//
// 四条语义：
//   - 目标租户必须是主体的在册成员（MembershipChecker，缺装配即拒绝——
//     fail-closed，不允许「装配缺失 = 免检切换」）；
//   - 目标租户必须在服务期（复用 TenantStatusChecker，与登录链路同口径）；
//   - 同会话重签 access token：会话与撤销版本不变，旧令牌自然过期，
//     生效租户回落 merchant_id（auth.Principal.CurrentTenant 的兜底链）——
//     切换是授权上下文变更，不是重新认证，不作废旧令牌；
//   - 写 auth.tenant_switch 审计（B 端成员关系变更的可追溯入口）。
func (s *AuthService) SwitchTenant(ctx context.Context, in SwitchTenantInput) (TenantSwitchResult, error) {
	if s.channel != domain.ChannelBiz {
		return TenantSwitchResult{}, apperr.BadRequest("tenant switch is only available on biz channel")
	}
	if s.tokens == nil {
		return TenantSwitchResult{}, apperr.Internal("token service is not configured", nil)
	}
	tenantID := strings.TrimSpace(in.TenantID)
	if tenantID == "" {
		return TenantSwitchResult{}, apperr.BadRequest("tenant_id is required")
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return TenantSwitchResult{}, apperr.BadRequest("session id is required")
	}
	// 会话必须真实存在、归属当前主体且未过期：切换凭证不能凭空的 sid 签发。
	session, err := s.sessions.FindByID(ctx, in.SessionID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return TenantSwitchResult{}, invalidToken()
		}
		return TenantSwitchResult{}, apperr.Internal("load session failed", err)
	}
	if session.AccountID != in.AccountID || !session.IsActive(s.clock.Now().UTC()) {
		return TenantSwitchResult{}, invalidToken()
	}
	account, err := s.loadActiveAccount(ctx, in.AccountID)
	if err != nil {
		return TenantSwitchResult{}, err
	}
	// 成员关系与租户可用性：两个校验都失败关闭（端口 nil / 仓储故障 → 拒绝）。
	if err := checkMembership(ctx, s.membership, tenantID, in.AccountID); err != nil {
		return TenantSwitchResult{}, err
	}
	if err := checkTenantForProvision(ctx, s.tenantCheck, tenantID); err != nil {
		return TenantSwitchResult{}, err
	}

	access, err := s.tokens.IssueAccessToken(account, in.SessionID, tenantID)
	if err != nil {
		return TenantSwitchResult{}, err
	}
	_ = s.emitAudit(ctx, audit.ActionAuthTenantSwitch, in.AccountID, tenantID, "account", in.AccountID,
		audit.OutcomeSuccess, "", in.TraceID, map[string]string{
			"previous_tenant": in.MerchantID,
			"session_id":      in.SessionID,
		})
	return TenantSwitchResult{
		AccessToken:   access,
		ExpiresIn:     int64(s.tokens.AccessTTL() / time.Second),
		CurrentTenant: tenantID,
	}, nil
}

// audit.ActionAuthTenantSwitch 租户切换审计动作（docs/audit/EVENTS.md）。
