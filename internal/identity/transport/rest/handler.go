// Package rest 提供 A / B 端共用的身份认证 HTTP Handler 与 DTO。
//
// 通道差异（端别、路由前缀）在 transport/admin 与 transport/biz 的路由层注入，
// 本包只负责「请求 → 用例 → 响应」的机械转换。
package rest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/app"
	"github.com/admin-frame/admin-frame/internal/identity/domain"
	"github.com/admin-frame/admin-frame/internal/shared/apiresp"
	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
	"github.com/admin-frame/admin-frame/pkg/crypto"
	pkghttpx "github.com/admin-frame/admin-frame/pkg/httpx"
	"github.com/admin-frame/admin-frame/pkg/pagination"
)

// HandlerConfig 是认证 Handler 的装配参数。
type HandlerConfig struct {
	// Auth 认证用例服务。
	Auth *app.AuthService
	// Channel 进程固定端别；不接受请求参数覆盖。
	Channel domain.Channel
	// ClientIP 可信代理解析器；nil 表示只使用 RemoteAddr。
	ClientIP *pkghttpx.ClientIPResolver
	// Hasher 是 IP / UA 摘要器；nil 时回退历史算法（仅限本地/tests）。
	Hasher *crypto.Hasher
	// EndpointLimiter 是 refresh / logout 的端点级限流器（SEC-04）；nil 时不限流。
	EndpointLimiter app.LoginLimiter
	// M2M 机器身份用例；nil 时不注册令牌与客户端管理端点（§21.2.6）。
	M2M *app.M2MService
	// M2MLimiter 是签发端点限流器（按 client_id，叠加单 IP）；nil 时回退 EndpointLimiter。
	M2MLimiter app.LoginLimiter
	// SCIM 是 SCIM 2.0 用例；nil 时不注册 A 端 SCIM 管理端点（§21.1.6）。
	SCIM *app.ScimService
}

// Handler 处理认证 HTTP 请求。
type Handler struct {
	auth       *app.AuthService
	accounts   *app.AccountService
	channel    domain.Channel
	ipResolver *pkghttpx.ClientIPResolver
	hasher     *crypto.Hasher
	limiter    app.LoginLimiter
	m2m        *app.M2MService
	m2mLimiter app.LoginLimiter
	scim       *app.ScimService
}

// NewHandler 构造认证 Handler。
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.Auth == nil {
		return nil, apperr.Internal("auth handler service is required", nil)
	}
	if err := cfg.Channel.Validate(); err != nil {
		return nil, err
	}
	accounts, err := app.NewAccountService(cfg.Auth)
	if err != nil {
		return nil, err
	}
	return &Handler{
		auth: cfg.Auth, accounts: accounts, channel: cfg.Channel,
		ipResolver: cfg.ClientIP, hasher: cfg.Hasher, limiter: cfg.EndpointLimiter,
		m2m: cfg.M2M, m2mLimiter: cfg.M2MLimiter, scim: cfg.SCIM,
	}, nil
}

// Login POST /{channel}/v1/auth/login
//
// A/B 端密码登录。账号不存在、密码错误、停用与锁定统一返回 40100；A 端可能返回
// MFA 挑战或强制改密中间态，这些中间态均不包含业务令牌。
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	var req loginRequest
	if err := DecodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	result, err := h.auth.Login(r.Context(), app.LoginInput{
		Subject:    req.Subject,
		MerchantID: req.MerchantID,
		Password:   req.Password,
		IPHash:     h.clientIPHash(r),
		UAHash:     h.clientUAHash(r),
		Device:     h.clientDevice(r),
		TraceID:    traceID,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	writeLoginResult(w, traceID, result)
}

// writeLoginResult 统一输出登录中间态与正式令牌。
func writeLoginResult(w http.ResponseWriter, traceID string, result app.LoginResult) {
	switch {
	case result.PasswordChangeRequired:
		apiresp.OK(w, passwordChangeRequiredResponse{
			PasswordChangeRequired: true,
			PasswordChangeTicket:   result.PasswordChangeTicket,
		}, traceID)
	case result.MFABindingRequired:
		apiresp.OK(w, mfaBindingResponse{MFABindingRequired: true, MFASetupTicket: result.MFASetupTicket}, traceID)
	case result.MFARequired:
		apiresp.OK(w, mfaChallengeResponse{MFARequired: true, MFATicket: result.MFATicket}, traceID)
	default:
		apiresp.OK(w, tokenResponseFrom(result.TokenPair), traceID)
	}
}

// Me GET /admin/v1/me
//
// 仅需认证，不额外要求权限点；角色与权限由 IAM 的 /iam/me/permissions 提供。
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	view, err := h.auth.Me(r.Context(), actor)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	// HasPassword / PhoneBound / PasswordUpdatedAt / MustChangePassword 供安全设置页
	// 展示真实状态，避免前端出现虚构的绑定信息。
	apiresp.OK(w, meResponseFrom(view), traceID)
}

// UpdateMe PATCH /admin/v1/me
//
// 只允许改展示昵称：字段缺失按 400 处理，传空串表示清除（前端回退登录主体）。
// 不要求额外权限点，与 GET /me 同级；成功直接返回更新后的完整资料。
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req updateMeRequest
	if err := DecodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	// 用指针区分「未传字段」与「显式清空」：前者是客户端缺陷，后者是合法操作。
	if req.DisplayName == nil {
		apiresp.FromError(w, apperr.BadRequest("display_name is required"), traceID)
		return
	}
	actor.TraceID = traceID
	view, err := h.auth.UpdateProfile(r.Context(), app.UpdateProfileInput{
		SessionActor: actor,
		DisplayName:  *req.DisplayName,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, meResponseFrom(view), traceID)
}

// SetupMFA POST /admin/v1/auth/mfa/setup
//
// 两种入口：Bearer（已登录绑定）或请求体中的 mfa_setup_ticket（登录前强制绑定）。
// 返回 TOTP 密钥与恢复码，仅本次响应可见；已绑定时返回 40900。
func (h *Handler) SetupMFA(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	var req mfaSetupRequest
	if err := DecodeOptionalJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var actor app.SessionActor
	if strings.TrimSpace(req.SetupTicket) == "" {
		var actorErr error
		actor, actorErr = actorFrom(r)
		if actorErr != nil {
			apiresp.FromError(w, actorErr, traceID)
			return
		}
	}
	result, err := h.auth.SetupMFA(r.Context(), app.MFASetupInput{
		SetupTicket: req.SetupTicket,
		Actor:       actor,
		IPHash:      h.clientIPHash(r),
		TraceID:     traceID,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, mfaSetupResponse{
		Secret:        result.Secret,
		OTPAuthURI:    result.OTPAuthURI,
		RecoveryCodes: result.RecoveryCodes,
	}, traceID)
}

// VerifyMFA POST /admin/v1/auth/mfa/verify
//
// 三种模式：mfa_ticket 完成两步登录；mfa_setup_ticket 完成强制绑定并签发令牌；
// 两者都不带时凭 Authorization 完成已登录账号的绑定确认（204）。
func (h *Handler) VerifyMFA(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	var req mfaVerifyRequest
	if err := DecodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var actor app.SessionActor
	if strings.TrimSpace(req.Ticket) == "" && strings.TrimSpace(req.SetupTicket) == "" {
		var actorErr error
		actor, actorErr = actorFrom(r)
		if actorErr != nil {
			apiresp.FromError(w, actorErr, traceID)
			return
		}
	}
	result, err := h.auth.VerifyMFA(r.Context(), app.MFAVerifyInput{
		Ticket:      req.Ticket,
		SetupTicket: req.SetupTicket,
		Code:        req.Code,
		Actor:       actor,
		IPHash:      h.clientIPHash(r),
		UAHash:      h.clientUAHash(r),
		Device:      h.clientDevice(r),
		TraceID:     traceID,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if result.BindingConfirmed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	apiresp.OK(w, tokenResponseFrom(result.TokenPair), traceID)
}

// DisableMFA POST /admin/v1/auth/mfa/disable
//
// 校验动态码或恢复码后解绑，并撤销该账号全部会话。
func (h *Handler) DisableMFA(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req mfaDisableRequest
	if err := DecodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := h.auth.DisableMFA(r.Context(), actor, req.Code); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ChangePassword POST /admin/v1/auth/password/change
//
// 两种入口：Bearer 自助改密（校验旧密码，204）；或凭 password_change_ticket
// 完成管理员重置后的强制改密（直接返回 TokenPair 或 MFA 中间态）。
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	var req changePasswordRequest
	if err := DecodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if strings.TrimSpace(req.PasswordChangeTicket) != "" {
		result, err := h.auth.CompletePasswordChange(r.Context(), app.CompletePasswordChangeInput{
			Ticket:      req.PasswordChangeTicket,
			NewPassword: req.NewPassword,
			IPHash:      h.clientIPHash(r),
			UAHash:      h.clientUAHash(r),
			Device:      h.clientDevice(r),
			TraceID:     traceID,
		})
		if err != nil {
			apiresp.FromError(w, err, traceID)
			return
		}
		writeLoginResult(w, traceID, result)
		return
	}
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := h.accounts.ChangePassword(r.Context(), app.ChangePasswordInput{
		Actor:       actor,
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	}); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DisableAccount POST /admin/v1/auth/accounts/{id}/disable
//
// 需要 identity:account:write。停用后递增 token_version、撤销全部会话；
// 已停用账号幂等返回 204，不重复写审计。
func (h *Handler) DisableAccount(w http.ResponseWriter, r *http.Request) {
	h.accountOperation(w, r, func(ctx context.Context, in app.AccountOperationInput) error {
		return h.accounts.Disable(ctx, in)
	})
}

// EnableAccount POST /admin/v1/auth/accounts/{id}/enable
//
// 需要 identity:account:write。恢复停用账号，不恢复旧会话，用户须重新登录；
// 目标账号未停用时返回 40900。
func (h *Handler) EnableAccount(w http.ResponseWriter, r *http.Request) {
	h.accountOperation(w, r, func(ctx context.Context, in app.AccountOperationInput) error {
		return h.accounts.Enable(ctx, in)
	})
}

// UnlockAccount POST /admin/v1/auth/accounts/{id}/unlock
//
// 需要 identity:account:write。解除失败锁定并写审计；已解锁账号幂等返回 204，
// 停用账号返回 40900。
func (h *Handler) UnlockAccount(w http.ResponseWriter, r *http.Request) {
	h.accountOperation(w, r, func(ctx context.Context, in app.AccountOperationInput) error {
		return h.accounts.Unlock(ctx, in)
	})
}

// ResetAccountMFA POST /admin/v1/auth/accounts/{id}/mfa/reset
//
// 需要 identity:account:write。删除 MFA 绑定、递增 token_version 并撤销全部会话，
// 用于用户丢失 TOTP 设备与恢复码的兜底；未绑定账号返回 40400。
func (h *Handler) ResetAccountMFA(w http.ResponseWriter, r *http.Request) {
	h.accountOperation(w, r, func(ctx context.Context, in app.AccountOperationInput) error {
		return h.accounts.ResetMFA(ctx, in)
	})
}

// accountOperation 统一解析管理员操作请求并执行用例。
func (h *Handler) accountOperation(
	w http.ResponseWriter,
	r *http.Request,
	apply func(context.Context, app.AccountOperationInput) error,
) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req accountActionRequest
	if err := DecodeOptionalJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := apply(r.Context(), app.AccountOperationInput{
		Actor:           actor,
		TargetAccountID: r.PathValue("id"),
		Reason:          req.Reason,
		MFACode:         req.MFACode,
	}); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAccounts GET /admin/v1/auth/accounts
//
// 需要 identity:account:read。分页列出本端账号（created_at 倒序），支持状态与
// 主体关键词筛选；软删账号同样返回并带 deleted_at。
func (h *Handler) ListAccounts(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	page, err := pagination.Parse(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	query := r.URL.Query()
	items, total, err := h.accounts.List(r.Context(), app.AccountListInput{
		Actor:        actor,
		Status:       strings.TrimSpace(query.Get("status")),
		Keyword:      strings.TrimSpace(query.Get("keyword")),
		SubjectExact: strings.TrimSpace(query.Get("subject")),
		Limit:        page.Limit(),
		Offset:       page.Offset(),
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	out := make([]accountDTO, 0, len(items))
	for _, item := range items {
		out = append(out, accountDTOFrom(item))
	}
	apiresp.OK(w, accountListResponse{Items: out, Total: total}, traceID)
}

// GetAccount GET /admin/v1/auth/accounts/{id}
//
// 需要 identity:account:read。返回账号状态、锁定到期、凭据与 MFA 状态；
// 软删账号同样可查，便于恢复。
func (h *Handler) GetAccount(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	detail, err := h.accounts.Get(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, accountDetailDTO{
		accountDTO:         accountDTOFrom(detail.AccountSummary),
		MFAEnabled:         detail.MFAEnabled,
		MustChangePassword: detail.MustChangePassword,
		HasPassword:        detail.HasPassword,
		PhoneBound:         detail.PhoneBound,
	}, traceID)
}

// LockAccount POST /admin/v1/auth/accounts/{id}/lock
//
// 需要 identity:account:write。按 duration_minutes 主动锁定（默认锁定策略），
// 递增 token_version 并撤销全部会话；已锁定账号幂等返回 204。
func (h *Handler) LockAccount(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req accountLockRequest
	if err := DecodeOptionalJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := h.accounts.Lock(r.Context(), app.LockAccountInput{
		AccountOperationInput: app.AccountOperationInput{
			Actor:           actor,
			TargetAccountID: r.PathValue("id"),
			Reason:          req.Reason,
			MFACode:         req.MFACode,
		},
		Duration: time.Duration(req.DurationMinutes) * time.Minute,
	}); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResetAccountPassword POST /admin/v1/auth/accounts/{id}/password/reset
//
// 返回一次性临时口令；响应不得缓存（no-store 由后续安全头统一补齐）。
func (h *Handler) ResetAccountPassword(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req accountActionRequest
	if err := DecodeOptionalJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	temporary, err := h.accounts.ResetPassword(r.Context(), app.AccountOperationInput{
		Actor:           actor,
		TargetAccountID: r.PathValue("id"),
		Reason:          req.Reason,
		MFACode:         req.MFACode,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, resetPasswordResponse{TemporaryPassword: temporary}, traceID)
}

// DeleteAccount DELETE /admin/v1/auth/accounts/{id}
//
// 需要 identity:account:write。软删账号、递增 token_version 并撤销全部会话；
// subject 仍占用唯一键，可通过 restore 恢复。
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	h.accountOperation(w, r, func(ctx context.Context, in app.AccountOperationInput) error {
		return h.accounts.Delete(ctx, in)
	})
}

// RestoreAccount POST /admin/v1/auth/accounts/{id}/restore
//
// 需要 identity:account:write。恢复软删账号为 active，不恢复旧会话，
// token_version 不递减；未删除账号幂等返回 204。
func (h *Handler) RestoreAccount(w http.ResponseWriter, r *http.Request) {
	h.accountOperation(w, r, func(ctx context.Context, in app.AccountOperationInput) error {
		return h.accounts.Restore(ctx, in)
	})
}

// ListAccountSessions GET /admin/v1/auth/accounts/{id}/sessions
//
// 需要 identity:account:read。按设备（family）分页列出目标账号会话，管理员视角 current 恒为 false；
// 只返回设备画像，不返回 IP / UA 原文或摘要。
func (h *Handler) ListAccountSessions(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	cursor, limit, err := sessionListQuery(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	page, err := h.accounts.ListTargetSessions(r.Context(), actor, r.PathValue("id"), cursor, limit)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, sessionListResponseFrom(page), traceID)
}

// RevokeAccountSession DELETE /admin/v1/auth/accounts/{id}/sessions/{sid}
//
// 需要 identity:account:session。撤销会话与刷新能力，并写会话撤销快照，
// 使该设备已签发的 Access Token 在 ≤60s 内失效。他人会话与不存在统一 404。
// 请求体可选，携带 mfa_code 用于特权操作二次认证。
func (h *Handler) RevokeAccountSession(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	var req accountActionRequest
	if err := DecodeOptionalJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := h.accounts.RevokeTargetSession(r.Context(), app.AccountOperationInput{
		Actor:           actor,
		TargetAccountID: r.PathValue("id"),
		Reason:          req.Reason,
		MFACode:         req.MFACode,
	}, r.PathValue("sid")); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeAccountSessions POST /admin/v1/auth/accounts/{id}/sessions/revoke-all
//
// 需要 identity:account:write。递增 token_version 并撤销目标账号全部会话，
// 旧 Access Token 在 ≤60s 内失效。
func (h *Handler) RevokeAccountSessions(w http.ResponseWriter, r *http.Request) {
	h.accountOperation(w, r, func(ctx context.Context, in app.AccountOperationInput) error {
		return h.accounts.RevokeTargetSessions(ctx, in)
	})
}

// Refresh POST /{channel}/v1/auth/refresh
//
// 凭一次性刷新令牌轮换并签发新令牌；已轮换令牌再次出现视为重放，
// 撤销整条会话链并返回 40100。
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	var req refreshRequest
	if err := DecodeJSON(r, &req); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if retryAfter, err := h.checkEndpointLimit(r, "refresh", req.RefreshToken); err != nil {
		setRetryAfter(w, retryAfter)
		apiresp.FromError(w, err, traceID)
		return
	}
	pair, err := h.auth.Refresh(r.Context(), app.RefreshInput{
		RefreshToken: req.RefreshToken,
		TraceID:      traceID,
		Device:       h.clientDevice(r),
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, tokenResponseFrom(pair), traceID)
}

// Logout POST /{channel}/v1/auth/logout
//
// 撤销当前会话并把当前 Access Token 的 jti 加入黑名单；幂等，重复登出返回 204。
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if retryAfter, err := h.checkEndpointLimit(r, "logout", actor.AccountID); err != nil {
		setRetryAfter(w, retryAfter)
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := h.auth.Logout(r.Context(), actor); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll POST /{channel}/v1/auth/logout-all
//
// 递增 token_version 并撤销账号全部会话；旧 Access Token 在 ≤60s 内失效。
func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := h.auth.LogoutAll(r.Context(), actor); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSessions GET /admin/v1/auth/sessions
//
// 按设备（family）分页列出本账号会话；只返回状态、时间与设备画像（不含 IP / UA 原文）。
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	cursor, limit, err := sessionListQuery(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	page, err := h.auth.ListSessions(r.Context(), app.SessionListInput{
		SessionActor: actor,
		Cursor:       cursor,
		Limit:        limit,
	})
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	apiresp.OK(w, sessionListResponseFrom(page), traceID)
}

// sessionListQuery 解析设备会话分页参数。
func sessionListQuery(r *http.Request) (cursor string, limit int, err error) {
	cursor = strings.TrimSpace(r.URL.Query().Get("cursor"))
	limit = 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return "", 0, apperr.BadRequest("invalid limit")
		}
		limit = parsed
	}
	return cursor, limit, nil
}

// sessionListResponseFrom 把设备会话分页结果转换为响应 DTO。
func sessionListResponseFrom(page app.SessionPage) sessionListResponse {
	items := make([]sessionDTO, 0, len(page.Items))
	for _, view := range page.Items {
		items = append(items, sessionDTO{
			ID:                view.ID,
			Current:           view.Current,
			Status:            view.Status,
			CreatedAt:         view.CreatedAt,
			LastUsedAt:        view.LastUsedAt,
			ExpiresAt:         view.ExpiresAt,
			AbsoluteExpiresAt: view.AbsoluteExpiresAt,
			IdleExpiresAt:     zeroAsNil(view.IdleExpiresAt),
			RevokedAt:         view.RevokedAt,
			DeviceLabel:       view.DeviceLabel,
			DeviceType:        view.DeviceType,
			OS:                view.OS,
			Browser:           view.Browser,
			IPMasked:          view.IPMasked,
		})
	}
	return sessionListResponse{Items: items, NextCursor: page.NextCursor}
}

// RevokeSession DELETE /admin/v1/auth/sessions/{id}
//
// 撤销本账号指定会话；他人会话与不存在统一返回 404，避免横向枚举。
// 被撤销会话的 Access Token 在 ≤60s 内失效；撤销当前会话时同时拉黑当前 jti。
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	traceID := apiresp.TraceIDFromContext(r.Context())
	actor, err := actorFrom(r)
	if err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	if err := h.auth.RevokeSession(r.Context(), actor, r.PathValue("id")); err != nil {
		apiresp.FromError(w, err, traceID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loginRequest 只接受主体、密码与 B 端商户上下文。
//
// channel / roles 由服务端推导；merchant_id 仅 B 端必填，用于租户内主体唯一。
type loginRequest struct {
	Subject    string `json:"subject"`
	Password   string `json:"password"`
	MerchantID string `json:"merchant_id,omitempty"`
}

// refreshRequest 刷新请求。
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// mfaChallengeResponse 密码正确但还需 MFA 时的响应；不包含任何令牌。
type mfaChallengeResponse struct {
	MFARequired bool   `json:"mfa_required"`
	MFATicket   string `json:"mfa_ticket"`
}

// mfaBindingResponse 密码正确但仍需完成首次绑定时的响应；同样不包含业务令牌。
type mfaBindingResponse struct {
	MFABindingRequired bool   `json:"mfa_binding_required"`
	MFASetupTicket     string `json:"mfa_setup_ticket"`
}

// mfaSetupRequest 绑定初始化请求；已登录绑定时可省略整个请求体。
type mfaSetupRequest struct {
	SetupTicket string `json:"mfa_setup_ticket,omitempty"`
}

// mfaSetupResponse 绑定初始化响应；密钥与恢复码只返回一次。
type mfaSetupResponse struct {
	Secret        string   `json:"secret"`
	OTPAuthURI    string   `json:"otpauth_uri"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// mfaVerifyRequest 校验请求：mfa_ticket 用于两步登录，mfa_setup_ticket 用于强制绑定；
// 两者都缺省时按已认证会话确认绑定。
type mfaVerifyRequest struct {
	Ticket      string `json:"mfa_ticket,omitempty"`
	SetupTicket string `json:"mfa_setup_ticket,omitempty"`
	Code        string `json:"code"`
}

// mfaDisableRequest 解绑请求。
type mfaDisableRequest struct {
	Code string `json:"code"`
}

// changePasswordRequest 改密请求。
//
// 两种模式：Bearer 自助改密需 old_password + new_password；
// 强制改密需 password_change_ticket + new_password（不得同时提交 old_password）。
type changePasswordRequest struct {
	OldPassword          string `json:"old_password,omitempty"`
	NewPassword          string `json:"new_password"`
	PasswordChangeTicket string `json:"password_change_ticket,omitempty"`
}

// accountActionRequest 管理员账号操作请求；reason 可选，写入审计。
//
// mfa_code 用于特权操作二次认证（启用 / 解锁 / 恢复 / 密码重置 / MFA 重置 /
// 删除 / 会话撤销）；admin_required=true 时缺失会返回 40000，错误码会按 MFA
// 失败计入锁定。
type accountActionRequest struct {
	Reason  string `json:"reason,omitempty"`
	MFACode string `json:"mfa_code,omitempty"`
}

// accountLockRequest 管理员锁定请求；duration_minutes 可选，默认锁定策略。
// mfa_code 为特权操作二次认证，admin_required=true 时必填。
type accountLockRequest struct {
	Reason          string `json:"reason,omitempty"`
	DurationMinutes int    `json:"duration_minutes,omitempty"`
	MFACode         string `json:"mfa_code,omitempty"`
}

// passwordChangeRequiredResponse 强制改密中间态；不包含任何业务令牌。
type passwordChangeRequiredResponse struct {
	PasswordChangeRequired bool   `json:"password_change_required"`
	PasswordChangeTicket   string `json:"password_change_ticket"`
}

// resetPasswordResponse 重置密码响应；临时口令只出现一次，禁止缓存与日志。
type resetPasswordResponse struct {
	TemporaryPassword string `json:"temporary_password"`
}

// accountDTO 账号列表项。
type accountDTO struct {
	ID           string     `json:"id"`
	Subject      string     `json:"subject"`
	Channel      string     `json:"channel"`
	Status       string     `json:"status"`
	MerchantID   string     `json:"merchant_id,omitempty"`
	TokenVersion int64      `json:"token_version"`
	LockedUntil  *time.Time `json:"locked_until,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// accountDetailDTO 账号详情；补充凭据与 MFA 状态。
type accountDetailDTO struct {
	accountDTO
	MFAEnabled         bool `json:"mfa_enabled"`
	MustChangePassword bool `json:"must_change_password"`
	HasPassword        bool `json:"has_password"`
	PhoneBound         bool `json:"phone_bound"`
}

// accountListResponse 账号列表响应。
type accountListResponse struct {
	Items []accountDTO `json:"items"`
	Total int          `json:"total"`
}

// accountDTOFrom 把用例视图转换为响应 DTO。
func accountDTOFrom(view app.AccountSummary) accountDTO {
	return accountDTO{
		ID:           view.ID,
		Subject:      view.Subject,
		Channel:      view.Channel.String(),
		Status:       view.Status.String(),
		MerchantID:   view.MerchantID,
		TokenVersion: view.TokenVersion,
		LockedUntil:  view.LockedUntil,
		DeletedAt:    view.DeletedAt,
		CreatedAt:    view.CreatedAt,
		UpdatedAt:    view.UpdatedAt,
	}
}

// tokenResponse 登录 / 刷新成功响应；令牌原文只在此出现一次。
type tokenResponse struct {
	AccessToken       string     `json:"access_token"`
	RefreshToken      string     `json:"refresh_token"`
	TokenType         string     `json:"token_type"`
	ExpiresIn         int64      `json:"expires_in"`
	AbsoluteExpiresAt *time.Time `json:"absolute_expires_at,omitempty"`
}

// meResponse 当前主体资料；不含角色与权限。
type meResponse struct {
	SubjectID string `json:"subject_id"`
	Subject   string `json:"subject"`
	// DisplayName 展示昵称；始终序列化，空串表示未设置（前端回退 subject）。
	DisplayName        string     `json:"display_name"`
	Channel            string     `json:"channel"`
	MerchantID         string     `json:"merchant_id"`
	Status             string     `json:"status"`
	MFAEnabled         bool       `json:"mfa_enabled"`
	HasPassword        bool       `json:"has_password"`
	PhoneBound         bool       `json:"phone_bound"`
	PasswordUpdatedAt  *time.Time `json:"password_updated_at,omitempty"`
	MustChangePassword bool       `json:"must_change_password"`
	TokenVersion       int64      `json:"token_version"`
}

// meResponseFrom 把用例视图转换为响应 DTO，供 GET /me 与 PATCH /me 复用。
func meResponseFrom(view app.MeView) meResponse {
	return meResponse{
		SubjectID:          view.SubjectID,
		Subject:            view.Subject,
		DisplayName:        view.DisplayName,
		Channel:            view.Channel.String(),
		MerchantID:         view.MerchantID,
		Status:             view.Status.String(),
		MFAEnabled:         view.MFAEnabled,
		HasPassword:        view.HasPassword,
		PhoneBound:         view.PhoneBound,
		PasswordUpdatedAt:  view.PasswordUpdatedAt,
		MustChangePassword: view.MustChangePassword,
		TokenVersion:       view.TokenVersion,
	}
}

// updateMeRequest 更新当前主体资料的请求体。
type updateMeRequest struct {
	// DisplayName 必填字段：nil 表示客户端未传（400），空串表示清除昵称。
	DisplayName *string `json:"display_name"`
}

// sessionDTO 会话列表项；不包含任何令牌或 IP / UA 原文。
type sessionDTO struct {
	ID         string    `json:"id"`
	Current    bool      `json:"current"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	// AbsoluteExpiresAt 设备绝对到期时间；刷新不会延长。
	AbsoluteExpiresAt time.Time  `json:"absolute_expires_at"`
	IdleExpiresAt     *time.Time `json:"idle_expires_at,omitempty"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	// DeviceLabel 可直接展示的设备名（如 Chrome · Windows）；无法识别时省略。
	DeviceLabel string `json:"device_label,omitempty"`
	// DeviceType / OS / Browser 设备画像枚举；仅用于展示，不参与鉴权。
	DeviceType string `json:"device_type,omitempty"`
	OS         string `json:"os,omitempty"`
	Browser    string `json:"browser,omitempty"`
	// IPMasked 脱敏客户端 IP；IPv4 保留前两段，IPv6 保留前四组。
	IPMasked string `json:"ip_masked,omitempty"`
}

// sessionListResponse 会话列表。
type sessionListResponse struct {
	Items      []sessionDTO `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

func tokenResponseFrom(pair app.TokenPair) tokenResponse {
	return tokenResponse{
		AccessToken:       pair.AccessToken,
		RefreshToken:      pair.RefreshToken,
		TokenType:         "Bearer",
		ExpiresIn:         pair.ExpiresIn,
		AbsoluteExpiresAt: zeroAsNil(pair.AbsoluteExpiresAt),
	}
}

// DecodeJSON 解码请求体；未知字段、尾随内容、错误 Content-Type 统一返回 40000（SEC-10）。
func DecodeJSON(r *http.Request, dst any) error {
	return decodeStrictJSON(r, dst, false)
}

// DecodeOptionalJSON 解码可选请求体：空 body 视为「未提供」，保持向后兼容。
func DecodeOptionalJSON(r *http.Request, dst any) error {
	return decodeStrictJSON(r, dst, true)
}

// decodeStrictJSON 是 A/B/C 三端共用的严格解码实现。
func decodeStrictJSON(r *http.Request, dst any, optional bool) error {
	if err := validateJSONContentType(r); err != nil {
		return err
	}
	if r.Body == nil {
		if optional {
			return nil
		}
		return apperr.BadRequest("invalid request body")
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return nil
		}
		return apperr.BadRequest("invalid request body")
	}
	// 拒绝多个 JSON 值 / 尾随非空内容，避免解析歧义影响审计与网关一致性。
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return apperr.BadRequest("request body must contain a single JSON object")
	}
	return nil
}

// validateJSONContentType 校验 Content-Type；缺省时不拒绝（兼容内部调用与旧客户端），
// 但显式声明的非 JSON 媒体类型一律拒绝。
func validateJSONContentType(r *http.Request) error {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return apperr.BadRequest("invalid content type")
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return apperr.BadRequest("content type must be application/json")
	}
	return nil
}

// checkEndpointLimit 对 refresh / logout 执行 IP + 主体摘要双维度限流（SEC-04）。
//
// 返回建议等待时长；限流器未注入时直接放行。
func (h *Handler) checkEndpointLimit(r *http.Request, endpoint, subject string) (time.Duration, error) {
	if h.limiter == nil {
		return 0, nil
	}
	keys := make([]string, 0, 2)
	if ip := h.clientIPHash(r); ip != "" {
		keys = append(keys, string(h.channel)+":"+endpoint+":ip:"+ip)
	}
	if ref := endpointSubjectRef(subject); ref != "" {
		keys = append(keys, string(h.channel)+":"+endpoint+":subject:"+ref)
	}
	if len(keys) == 0 {
		keys = append(keys, string(h.channel)+":"+endpoint+":global")
	}
	var retryAfter time.Duration
	for _, key := range keys {
		allowed, after := h.limiter.Allow(key)
		if allowed {
			continue
		}
		if after > retryAfter {
			retryAfter = after
		}
		return retryAfter, apperr.TooManyRequests(endpoint + " rate limit exceeded")
	}
	return 0, nil
}

// endpointSubjectRef 返回主体（刷新令牌 / 账号 ID）的短摘要，禁止把原文写入限流键。
func endpointSubjectRef(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])[:16]
}

// setRetryAfter 按秒向上取整写入 Retry-After。
func setRetryAfter(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter <= 0 {
		return
	}
	seconds := int((retryAfter + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}

// actorFrom 从鉴权上下文构造用例入参；缺少 Principal 视为未认证。
func actorFrom(r *http.Request) (app.SessionActor, error) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		return app.SessionActor{}, apperr.Unauthorized("missing authentication context")
	}
	return app.SessionActor{
		AccountID:      p.SubjectID,
		SessionID:      p.SessionID,
		TokenID:        p.TokenID,
		TokenExpiresAt: p.TokenExpiresAt,
		MerchantID:     p.MerchantID,
		TraceID:        apiresp.TraceIDFromContext(r.Context()),
	}, nil
}

// clientIPHash 返回客户端 IP 摘要；配置 hash_key 时使用带密钥 HMAC。
func (h *Handler) clientIPHash(r *http.Request) string {
	return app.ClientRef(h.hasher, app.HashDomainClientIP, h.clientIP(r))
}

// clientUAHash 返回 User-Agent 摘要；与 IP 使用不同域，禁止交叉复用。
func (h *Handler) clientUAHash(r *http.Request) string {
	return app.ClientRef(h.hasher, app.HashDomainClientUA, r.UserAgent())
}

// clientDevice 从原始 UA / IP 派生可展示设备画像；原文不落库。
func (h *Handler) clientDevice(r *http.Request) domain.DeviceInfo {
	return domain.ParseDeviceInfo(r.UserAgent(), h.clientIP(r))
}

// clientIP 使用可信代理解析器获取客户端 IP；只用于计算不可逆摘要，不直接落库。
func (h *Handler) clientIP(r *http.Request) string {
	return h.clientIPResolver().ClientIP(r)
}

// clientIPResolver 返回解析器；nil 接收者等价于不信任任何代理头（只使用 RemoteAddr）。
func (h *Handler) clientIPResolver() *pkghttpx.ClientIPResolver {
	if h == nil {
		return nil
	}
	return h.ipResolver
}

// zeroAsNil 把零值时间转换为 null，避免响应出现 0001-01-01。
func zeroAsNil(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
