// Package persistence 提供 IAM 仓储的内存实现与 SQL 实现。
//
// 内存实现用于单测、本地演示与 `iam.mode=off` 下的只读校验；
// SQL 实现（MySQL 8.0）由发布流水线迁移建表后接入，两条实现必须满足
// internal/iam/domain 中仓储端口定义的同一组语义：
//   - 未找到返回 apperr.NotFound；
//   - 乐观锁冲突返回 apperr.Conflict；
//   - B 端查询必须带 merchant_id，空值不得放宽条件；
//   - 读方法返回副本，调用方修改返回值不得影响仓储内部状态。
package persistence
