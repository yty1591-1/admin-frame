// Package security 提供与端别绑定的认证（AuthN）与授权（AuthZ）实现。
//
// 本包只负责“令牌校验 / 权限点判定 / 主体注入”，不包含具体业务规则：
// 角色授予哪些权限点由 cmd 装配期注入（见 rbac.Checker），演示角色表也在
// cmd 中构造，避免平台层与业务域耦合（ARCHITECTURE §5.6）。
//
// 依赖方向：security → shared/auth、shared/apperr、shared/httpx、pkg/*；
// 不得反向依赖 internal/platform 或任何业务域。
package security
