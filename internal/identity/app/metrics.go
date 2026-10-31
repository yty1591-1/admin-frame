package app

import (
	"context"
	"time"

	"github.com/admin-frame/admin-frame/internal/identity/domain"
)

// Metrics 是身份域核心指标的观测端口（IDN-P1-03）。
//
// 约定：
//   - 实现必须并发安全、非阻塞，且不得返回错误；指标失败不得影响主流程；
//   - 标签只允许低基数枚举值，禁止写入账号、手机号、令牌等 PII 或原文；
//   - 未注入实现时所有上报为 no-op。
type Metrics interface {
	// ObserveLogin 记录一次登录尝试及其耗时；result=success|failure|challenge，reason 为粗粒度原因。
	ObserveLogin(ctx context.Context, channel domain.Channel, result, reason string, duration time.Duration)
	// ObserveLoginTxDuration 记录登录事务阶段耗时（不含事务外密码哈希）。
	ObserveLoginTxDuration(ctx context.Context, channel domain.Channel, duration time.Duration)
	// ObserveLockout 记录一次账号锁定迁移。
	ObserveLockout(ctx context.Context, channel domain.Channel)
	// ObserveMFAVerify 记录一次 MFA 校验结果。
	ObserveMFAVerify(ctx context.Context, result string)
	// ObserveMFAVerifyDuration 记录一次 MFA 校验耗时（OPS-03）。
	ObserveMFAVerifyDuration(ctx context.Context, result string, duration time.Duration)
	// ObserveMFARecoveryUsed 记录一次恢复码使用及剩余数量。
	ObserveMFARecoveryUsed(ctx context.Context, remaining int)
	// ObserveRefresh 记录一次刷新结果。
	ObserveRefresh(ctx context.Context, result string)
	// ObserveRefreshDuration 记录一次刷新耗时（OPS-03）。
	ObserveRefreshDuration(ctx context.Context, result string, duration time.Duration)
	// ObserveStepUpDuration 记录一次管理员二次 MFA 耗时（OPS-03）。
	ObserveStepUpDuration(ctx context.Context, result string, duration time.Duration)
	// ObserveTokenReuse 记录一次刷新令牌重放。
	ObserveTokenReuse(ctx context.Context)
	// ObserveSessionRevoke 记录一次设备撤销；reason 为 RevokeReason，result=success|failure。
	ObserveSessionRevoke(ctx context.Context, reason, result string)
	// ObserveSMSSend 记录一次短信验证码发送结果。
	ObserveSMSSend(ctx context.Context, result string)
	// ObserveOAuthCallback 记录一次 OAuth 回调结果。
	ObserveOAuthCallback(ctx context.Context, provider, result string)
	// ObserveOAuthCallbackDuration 记录一次 OAuth 回调耗时（OPS-03）。
	ObserveOAuthCallbackDuration(ctx context.Context, provider, result string, duration time.Duration)
}

// 指标结果与原因的稳定枚举值。
const (
	// MetricResultSuccess 成功。
	MetricResultSuccess = "success"
	// MetricResultFailure 失败。
	MetricResultFailure = "failure"
	// MetricResultChallenge 需要继续挑战（MFA / 强制改密），不含业务令牌。
	MetricResultChallenge = "challenge"

	// MetricReasonOK 正常完成。
	MetricReasonOK = "ok"
	// MetricReasonBadCredential 账号不存在或凭据错误（对客户端统一文案）。
	MetricReasonBadCredential = "bad_credential" //nolint:gosec // G101: 指标原因字面量，不是凭证
	// MetricReasonMFARequired 需要完成两步验证。
	MetricReasonMFARequired = "mfa_required"
	// MetricReasonMFABindingRequired 需要先绑定 MFA。
	MetricReasonMFABindingRequired = "mfa_binding_required"
	// MetricReasonPasswordChangeRequired 管理员重置密码后必须先改密。
	MetricReasonPasswordChangeRequired = "password_change_required"
	// MetricReasonRateLimited 触发限流。
	MetricReasonRateLimited = "rate_limited"
	// MetricReasonUnavailable 依赖不可用（50300）。
	MetricReasonUnavailable = "unavailable"
	// MetricReasonInternal 内部错误。
	MetricReasonInternal = "internal"
)
