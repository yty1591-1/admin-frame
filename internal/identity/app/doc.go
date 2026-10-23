// Package app 编排身份域用例：凭据策略、失败锁定与登录限流。
//
// 阶段 1 只交付与 HTTP 无关的策略内核（docs/identity-authn.md T3）：
//   - PasswordService 复用 pkg/password 的 Argon2id，并执行密码强度策略；
//   - LoginGuard 负责账号状态准入、失败计数与阈值锁定，限流通过消费侧端口注入。
//
// 令牌签发、会话轮换与审计事务在阶段 2 / 5 接入；本包不依赖 transport 与 platform。
package app
