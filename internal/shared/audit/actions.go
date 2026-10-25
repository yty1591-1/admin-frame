package audit

// 稳定 action 常量，须与 docs/audit/EVENTS.md 同步（AUD-01）。
const (
	ActionAuthLogin           = "auth.login"
	ActionAuthLoginFailed     = "auth.login_failed"
	ActionAuthAccountCreated  = "auth.account.created"
	ActionAuthAccountLocked   = "auth.account.locked"
	ActionAuthAccountDisabled = "auth.account.disabled"
	ActionAuthLogout          = "auth.logout"
	ActionAuthTokenRefresh    = "auth.token.refresh" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthTokenReused     = "auth.token.reused"  //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthTokenRevoked    = "auth.token.revoked" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthMFABound        = "auth.mfa.bound"
	ActionAuthMFAFailed       = "auth.mfa.failed"
	ActionAuthMFADisabled     = "auth.mfa.disabled"
	ActionRBACRoleChange      = "rbac.role_change"
	ActionRBACBindingChange   = "rbac.binding_change"
	ActionRBACBreakglass      = "rbac.breakglass"
	ActionConfigChange        = "config.change"
	ActionDataExport          = "data.export"
	ActionDataQueryScope      = "data.query_scope"
	ActionPaymentCapture      = "payment.capture"
	ActionPaymentRefund       = "payment.refund"
)

// KnownActions 返回目录内全部已知 action（供门禁与单测）。
func KnownActions() []string {
	return []string{
		ActionAuthLogin,
		ActionAuthLoginFailed,
		ActionAuthAccountCreated,
		ActionAuthAccountLocked,
		ActionAuthAccountDisabled,
		ActionAuthLogout,
		ActionAuthTokenRefresh,
		ActionAuthTokenReused,
		ActionAuthTokenRevoked,
		ActionAuthMFABound,
		ActionAuthMFAFailed,
		ActionAuthMFADisabled,
		ActionRBACRoleChange,
		ActionRBACBindingChange,
		ActionRBACBreakglass,
		ActionConfigChange,
		ActionDataExport,
		ActionDataQueryScope,
		ActionPaymentCapture,
		ActionPaymentRefund,
	}
}
