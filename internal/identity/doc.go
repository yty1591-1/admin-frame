// Package identity 承载身份域：账号、凭据、会话、MFA 与登录审计。
//
// 分层（docs/identity-authn.md §13）：
//   - domain：聚合、状态机、仓储端口与领域错误，不依赖框架与 internal/platform；
//   - app：登录 / 刷新 / 登出策略编排，事务与审计在阶段 2 / 5 接入；
//   - infrastructure：MySQL 与内存仓储、Redis 撤销与验证码存储；
//   - transport：三端认证端点，通道无关 Handler 复用。
//
// 与 IAM 的边界：身份域只回答「你是谁」，角色 / 权限 / 数据范围仍归 internal/iam；
// 令牌不携带角色，权限一律以 IAM 快照为准，避免撤权滞后。
package identity
