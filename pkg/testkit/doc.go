// Package testkit 为集成测试提供不感知业务的外部依赖夹具：TEST_* 环境变量、
// 就绪探测、Redis 与对象存储装配、仓库根定位。
//
// 设计约束（TESTING_STANDARDS TEST-05 / TEST-07）：
//   - 只读取 TEST_* 环境变量，不内置端口、口令或真实云凭据；
//   - 变量缺失时显式 t.Skipf 并说明原因，禁止静默通过；
//   - 连接串与密钥不得写入仓库、日志或错误消息；
//   - 本包只依赖 pkg/*，不得 import internal/*。
//
// MySQL 夹具需要跑仓库迁移并种入引导管理员，会依赖 internal/identity 的实现，
// 因此不在本包：见 internal/testdb 的 MySQL / Migrate。
package testkit
