// Package testdb 为集成 / E2E 测试提供 MySQL 夹具：开库、执行仓库迁移、种入引导管理员。
//
// 与 pkg/testkit 的分工：
//   - pkg/testkit 只放不感知业务的可复用夹具（TEST_* 环境变量、Redis、对象存储、
//     仓库根定位），不得 import internal/*；
//   - 本包依赖 internal/identity 的引导与持久化实现，属应用内测试支撑，因此放在
//     internal 下，避免 pkg 反向依赖业务域（ARCHITECTURE §6.1 依赖方向）。
//
// 约束：只允许测试进程使用；生产环境迁移只走 cmd/migrate 或部署 Job。
package testdb
