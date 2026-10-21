// Package domain 包含 IAM 的角色、绑定、权限集合与数据范围模型，以及仓储端口。
//
// 分层约束（ARCHITECTURE §6.2）：
//   - 本包不得 import app / transport / infrastructure / platform / database/sql；
//   - 错误以 internal/shared/apperr 的稳定业务码向上抛出，并保留可用 errors.Is 匹配的哨兵；
//   - 时间一律 UTC；版本号用于乐观锁，由调用方传入 expectedVersion。
//
// 与 internal/iam/catalog 的分工：
//   - catalog 描述「权限点目录」（含 both 端别、菜单形态、数据范围标记），只在授权校验时使用；
//   - 本包描述「角色与绑定」（端别只有 admin / biz），不依赖 catalog，避免目录变更影响领域不变量。
//
// 目录相关的交叉校验（权限码是否存在、是否可授予到该端、data_bearing 是否已配数据范围）
// 属于 app 层职责，本包只保证结构与状态自洽。
package domain
