// Package authn 是鉴权中间件的装配点：按 auth.mode 与环境返回 demo 或 JWT
// 校验中间件，并拒绝非法组合（如 prod + demo）。
//
// 边界：本包只做模式分派与装配校验，令牌校验实现在 internal/security；
// 角色到权限点的映射由 cmd 在装配期注入，平台不定义业务权限语义。
package authn
