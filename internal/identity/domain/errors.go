package domain

import (
	"errors"
	"fmt"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
)

// 领域哨兵错误。调用方可用 errors.Is 精确判断失败原因，
// 同时 apperr 仍提供稳定的 HTTP 业务码映射（docs/identity-authn.md §3.4）。
var (
	// ErrInvalidChannel 端别不是 admin / biz / c。
	ErrInvalidChannel = errors.New("invalid channel")
	// ErrEmptyInput 必填字段为空。
	ErrEmptyInput = errors.New("required field is empty")
	// ErrInvalidAccount 账号字段自相矛盾（如非 B 端携带商户、主体过长）。
	ErrInvalidAccount = errors.New("invalid account")
	// ErrInvalidAccountStatus 账号状态取值非法。
	ErrInvalidAccountStatus = errors.New("invalid account status")
	// ErrInvalidAccountTransition 账号状态迁移不被允许。
	ErrInvalidAccountTransition = errors.New("invalid account transition")
	// ErrAccountDeleted 账号已软删。
	ErrAccountDeleted = errors.New("account deleted")
	// ErrAccountDisabled 账号已停用，任何登录路径均拒绝。
	ErrAccountDisabled = errors.New("account disabled")
	// ErrAccountLocked 账号处于锁定期，未到期前拒绝登录。
	ErrAccountLocked = errors.New("account locked")
	// ErrVersionConflict 乐观锁版本不匹配。
	ErrVersionConflict = errors.New("version conflict")
	// ErrInvalidCredentialType 凭据类型不受支持。
	ErrInvalidCredentialType = errors.New("invalid credential type")
	// ErrInvalidCredential 凭据字段非法（哈希为空、超长等）。
	ErrInvalidCredential = errors.New("invalid credential")
	// ErrInvalidSession 会话字段非法（家庭 ID / 刷新哈希为空、已过期）。
	ErrInvalidSession = errors.New("invalid session")
	// ErrInvalidSessionStatus 会话状态取值非法。
	ErrInvalidSessionStatus = errors.New("invalid session status")
	// ErrSessionNotActive 会话已轮换、撤销或过期，不能继续使用。
	ErrSessionNotActive = errors.New("session not active")
	// ErrSessionExpired 会话已过期。
	ErrSessionExpired = errors.New("session expired")
	// ErrInvalidMFA MFA 绑定字段非法。
	ErrInvalidMFA = errors.New("invalid mfa enrollment")
	// ErrMFAAlreadyConfirmed MFA 已确认，不能重复确认。
	ErrMFAAlreadyConfirmed = errors.New("mfa already confirmed")
	// ErrInvalidLoginOutcome 登录结果取值非法。
	ErrInvalidLoginOutcome = errors.New("invalid login outcome")
	// ErrInvalidLoginAttempt 登录尝试字段非法。
	ErrInvalidLoginAttempt = errors.New("invalid login attempt")
	// ErrInvalidCredentials 凭据不匹配统一错误，对外不区分账号不存在与密码错误。
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrMFARequired 凭据已通过但 MFA 未完成。
	ErrMFARequired = errors.New("mfa required")
	// ErrTokenReplay 刷新令牌重放，需撤销整条会话链。
	ErrTokenReplay = errors.New("refresh token replay")
)

// errInvalid 构造参数错误（HTTP 400 / 业务码 40000）。
//
// 使用 errors.Join 同时挂接领域哨兵与 apperr 语义哨兵，
// 使 errors.Is(err, ErrInvalidSession) 与 errors.Is(err, apperr.ErrBadRequest) 都成立。
func errInvalid(sentinel error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return apperr.New(apperr.CodeBadRequest, msg, errors.Join(sentinel, apperr.ErrBadRequest))
}

// errConflict 构造状态冲突错误（HTTP 409 / 业务码 40900）。
func errConflict(sentinel error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return apperr.New(apperr.CodeConflict, msg, errors.Join(sentinel, apperr.ErrConflict))
}

// errUnauthorized 构造统一认证失败错误（HTTP 401 / 业务码 40100）。
//
// 对外文案必须保持统一，真实原因只通过 errors.Is 在进程内区分并写入审计。
func errUnauthorized(sentinel error, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return apperr.New(apperr.CodeUnauthorized, msg, errors.Join(sentinel, apperr.ErrUnauthorized))
}
