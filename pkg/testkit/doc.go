// Package testkit 为集成测试提供外部依赖（MySQL / Redis / MinIO）的装配、就绪探测与跳过策略。
//
// 设计约束（TESTING_STANDARDS TEST-05 / TEST-07）：
//   - 只读取 TEST_* 环境变量，不内置端口、口令或真实云凭据；
//   - 变量缺失时显式 t.Skipf 并说明原因，禁止静默通过；
//   - 连接串与密钥不得写入仓库、日志或错误消息；
//   - 本包只依赖 pkg/*，不得 import internal/*。
package testkit
