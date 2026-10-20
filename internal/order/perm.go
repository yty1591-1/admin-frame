package order

// 订单域权限点（rbac.Permission 字符串）。
//
// 放在域根包而非 platform：权限点属于业务契约，由 cmd 在装配期授予角色，
// transport 只引用常量，平台层不感知（ARCHITECTURE §5.6）。
const (
	// PermRead 查询订单（A/B 端）。
	PermRead = "order:read"
	// PermWrite 变更订单（取消等，A/B 端）。
	PermWrite = "order:write"
)
