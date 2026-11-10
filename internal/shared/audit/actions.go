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
	ActionAuthSAMLAuthorize   = "auth.saml.authorize"
	ActionAuthSAMLCallback    = "auth.saml.callback"
	// M2M 机器身份（§21.2.5）：签发成功 / 失败与客户端生命周期。
	ActionAuthM2MTokenIssued    = "auth.m2m.token.issued" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthM2MTokenDenied    = "auth.m2m.token.denied" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthM2MClientCreated  = "auth.m2m.client.created"
	ActionAuthM2MClientUpdated  = "auth.m2m.client.updated"
	ActionAuthM2MClientDisabled = "auth.m2m.client.disabled"
	ActionAuthM2MClientEnabled  = "auth.m2m.client.enabled"
	ActionAuthM2MClientDeleted  = "auth.m2m.client.deleted"
	ActionAuthM2MSecretRotated  = "auth.m2m.secret.rotated"
	// SCIM 2.0 目录同步（§21.1.5）：资源变更与令牌生命周期。
	ActionAuthScimUserCreated  = "auth.scim.user.created"
	ActionAuthScimUserUpdated  = "auth.scim.user.updated"
	ActionAuthScimUserDeact    = "auth.scim.user.deactivated"
	ActionAuthScimUserDeleted  = "auth.scim.user.deleted"
	ActionAuthScimGroupUpdated = "auth.scim.group.updated"
	ActionAuthScimTokenCreated = "auth.scim.token.created" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthScimTokenRevoked = "auth.scim.token.revoked" //nolint:gosec // G101: 审计动作名，不是凭证
	ActionAuthRateLimited      = "auth.rate_limited"
	ActionAuthSessionEvicted   = "auth.session.evicted"
	ActionAuthTenantSwitch     = "auth.tenant_switch"
	ActionRBACRoleChange       = "rbac.role_change"
	ActionRBACBindingChange    = "rbac.binding_change"
	ActionRBACBreakglass       = "rbac.breakglass"
	ActionRBACMenuChange       = "rbac.menu_change"
	ActionRBACSodChange        = "rbac.sod_change"
	// ActionRBACCertification 访问复核：创建 / 激活 / 关闭活动、裁决任务与证据导出。
	ActionRBACCertification = "rbac.certification"
	ActionConfigChange      = "config.change"
	ActionDataExport        = "data.export"
	ActionDataQueryScope    = "data.query_scope"
	ActionPaymentCapture    = "payment.capture"
	ActionPaymentRefund     = "payment.refund"
	// ActionAuditQuery 审计事件查询（ENT-04）；记录查询范围摘要，不记过滤结果原文。
	ActionAuditQuery = "audit.query"
	// ActionAuditExport 审计事件导出（ENT-04）；含导出范围、条数与操作者。
	ActionAuditExport = "audit.export"
	// ActionAuditRetentionPurge 审计到期清理（ENT-04）；清理动作本身必须留痕。
	ActionAuditRetentionPurge = "audit.retention_purge"
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
		ActionAuthSAMLAuthorize,
		ActionAuthSAMLCallback,
		ActionAuthM2MTokenIssued,
		ActionAuthM2MTokenDenied,
		ActionAuthM2MClientCreated,
		ActionAuthM2MClientUpdated,
		ActionAuthM2MClientDisabled,
		ActionAuthM2MClientEnabled,
		ActionAuthM2MClientDeleted,
		ActionAuthM2MSecretRotated,
		ActionAuthScimUserCreated,
		ActionAuthScimUserUpdated,
		ActionAuthScimUserDeact,
		ActionAuthScimUserDeleted,
		ActionAuthScimGroupUpdated,
		ActionAuthScimTokenCreated,
		ActionAuthScimTokenRevoked,
		ActionAuthRateLimited,
		ActionAuthSessionEvicted,
		ActionAuthTenantSwitch,
		ActionRBACRoleChange,
		ActionRBACBindingChange,
		ActionRBACBreakglass,
		ActionRBACMenuChange,
		ActionRBACSodChange,
		ActionRBACCertification,
		ActionConfigChange,
		ActionDataExport,
		ActionDataQueryScope,
		ActionPaymentCapture,
		ActionPaymentRefund,
		ActionAuditQuery,
		ActionAuditExport,
		ActionAuditRetentionPurge,
	}
}
