// Package domain 包含身份域的聚合、状态机与仓储端口。
//
// 分层约束（ARCHITECTURE §6.2）：
//   - 本包不得 import app / transport / infrastructure / platform / database/sql；
//   - 错误以 internal/shared/apperr 的稳定业务码向上抛出，并保留可用 errors.Is 匹配的哨兵；
//   - 时间一律 UTC，由调用方传入 now，便于状态迁移用例确定性推进时间；
//   - 版本号用于乐观锁，由调用方传入 expectedVersion。
//
// 设计要点（docs/identity-authn.md §3）：
//   - Account 的 token_version 只增不减，表达账号级撤销；Version 是独立乐观锁字段；
//   - Session 的一次性刷新由 active → rotated 表达，重放由仓储整链撤销；
//   - 登录尝试只保存不可逆摘要，禁止落明文手机号 / 邮箱 / 密码。
package domain
