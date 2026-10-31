package audit

// 稳定 action 常量，须与 docs/audit/EVENTS.md 同步（AUD-01）。
const (
	ActionAuthLogin           = "auth.login"
	ActionAuthLoginFailed     = "auth.login_failed"
	ActionAuthAccountCreated  = "auth.account.created"
	ActionAuthAccountLocked   = "auth.account.locked"
	ActionAuthAccountDisabled = "auth.account.disabled"
	ActionAuthAccountEnabled  = "auth.account.enabled"
	ActionAuthAccountUnlocked = "auth.account.unlocked"
	ActionAuthAccountDeleted  = "auth.account.deleted"
	ActionAuthAccountRestored = "auth.account.restored"
	ActionAuthPasswordChanged = "auth.password.changed"
	ActionAuthPasswordReset   = "auth.password.reset"
	ActionAuthProfileUpdated  = "auth.profile.updated"
	ActionAuthPhoneRebound    = "auth.phone.rebound"
	ActionAuthAccountRead     = "auth.account.read"
	ActionAuthSessionRead     = "auth.session.read"
	ActionAuthLogout          = "auth.logout"
	ActionAuthTokenRefresh    = "auth.token.refresh" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthTokenReused     = "auth.token.reused"  //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthTokenRevoked    = "auth.token.revoked" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthMFABound        = "auth.mfa.bound"
	ActionAuthMFAFailed       = "auth.mfa.failed"
	ActionAuthMFADisabled     = "auth.mfa.disabled"
	ActionAuthMFAReset        = "auth.mfa.reset"
	ActionAuthMFASetup        = "auth.mfa.setup"
	ActionAuthMFARecoveryUsed = "auth.mfa.recovery_used"
	ActionAuthMFATicketReplay = "auth.mfa.ticket_replay"
	ActionAuthSMS             = "auth.sms.send"
	ActionAuthOAuthAuthorize  = "auth.oauth.authorize"
	ActionAuthOAuthCallback   = "auth.oauth.callback"
	ActionAuthRateLimited     = "auth.rate_limited"
	ActionAuthSessionEvicted  = "auth.session.evicted"
	ActionRBACRoleChange      = "rbac.role_change"
	ActionRBACBindingChange   = "rbac.binding_change"
	ActionRBACBreakglass      = "rbac.breakglass"
	ActionRBACMenuChange      = "rbac.menu_change"
	ActionRBACSodChange       = "rbac.sod_change"
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
		ActionAuthAccountEnabled,
		ActionAuthAccountUnlocked,
		ActionAuthAccountDeleted,
		ActionAuthAccountRestored,
		ActionAuthPasswordChanged,
		ActionAuthPasswordReset,
		ActionAuthProfileUpdated,
		ActionAuthPhoneRebound,
		ActionAuthAccountRead,
		ActionAuthSessionRead,
		ActionAuthLogout,
		ActionAuthTokenRefresh,
		ActionAuthTokenReused,
		ActionAuthTokenRevoked,
		ActionAuthMFABound,
		ActionAuthMFAFailed,
		ActionAuthMFADisabled,
		ActionAuthMFAReset,
		ActionAuthMFASetup,
		ActionAuthMFARecoveryUsed,
		ActionAuthMFATicketReplay,
		ActionAuthSMS,
		ActionAuthOAuthAuthorize,
		ActionAuthOAuthCallback,
		ActionAuthRateLimited,
		ActionAuthSessionEvicted,
		ActionRBACRoleChange,
		ActionRBACBindingChange,
		ActionRBACBreakglass,
		ActionRBACMenuChange,
		ActionRBACSodChange,
		ActionConfigChange,
		ActionDataExport,
		ActionDataQueryScope,
		ActionPaymentCapture,
		ActionPaymentRefund,
	}
}
