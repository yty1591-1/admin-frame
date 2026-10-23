// Package persistence 提供身份域仓储的内存实现与 MySQL 实现。
//
// 两条实现必须满足 internal/identity/domain 中仓储端口的同一组语义：
//   - 未找到返回 apperr.NotFound；
//   - 乐观锁冲突返回 apperr.Conflict；
//   - 唯一键（channel+subject、refresh_token_hash、account+credential_type）冲突返回 apperr.Conflict；
//   - 读方法返回副本，调用方修改返回值不得影响仓储内部状态。
//
// 迁移只由 cmd/migrate 或部署 Job 执行（DATA_STANDARDS）；本包不做建表或版本校验。
package persistence
