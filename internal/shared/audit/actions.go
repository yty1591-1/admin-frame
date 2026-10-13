package audit

// 稳定 action 常量，须与 docs/audit/EVENTS.md 同步（AUD-01）。
const (
	ActionAuthLogin       = "auth.login"
	ActionAuthLoginFailed = "auth.login_failed"
	ActionRBACRoleChange  = "rbac.role_change"
	ActionConfigChange    = "config.change"
	ActionDataExport      = "data.export"
	ActionDataQueryScope  = "data.query_scope"
	ActionPaymentCapture  = "payment.capture"
	ActionPaymentRefund   = "payment.refund"
)

// KnownActions 返回目录内全部已知 action（供门禁与单测）。
func KnownActions() []string {
	return []string{
		ActionAuthLogin,
		ActionAuthLoginFailed,
		ActionRBACRoleChange,
		ActionConfigChange,
		ActionDataExport,
		ActionDataQueryScope,
		ActionPaymentCapture,
		ActionPaymentRefund,
	}
}
