// Package rbac 提供轻量权限判断原语：角色→权限点、通配符与默认拒绝。
//
// 设计边界（对齐架构 AuthZ / SEC-14 / API 40300）：
//   - 不得依赖 internal/；业务码 40300、租户隔离、菜单树等留 platform / IAM 域
//   - 默认拒绝：未显式授予则不允许
//   - 权限点命名建议 resource:action（如 order:read）；支持 * 与 resource:* 通配
//   - 不实现 ABAC、条件策略、权限继承图；复杂策略放业务层
//   - HTTP 中间件只做判定；OnDenied / OnUnauthenticated 由调用方注入信封
//
// 典型用法：
//
//	c := rbac.New(rbac.WithSuperRoles("admin"))
//	c.GrantRole("merchant_staff", "order:read", "order:write")
//	if !c.Allow(rbac.Subject{Roles: roles}, "order:read") { ... }
//
//	h := rbac.Require(rbac.RequireConfig{Checker: c, Permission: "order:read", ...})(next)
package rbac
