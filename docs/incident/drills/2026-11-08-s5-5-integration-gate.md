# S5-5 门禁演练：移除集成测试阻断合并 — 2026-11-08

| 字段 | 内容 |
|---|---|
| 场景 | S5-5 退出条件的另一半：「人为移除集成测试必须阻断合并」（阈值注入 101% → exit 1 已于此前验证并记录在 IAM 计划） |
| 形态 | **负向门禁演练（本地）**：物理移除测试文件 / 整包后运行 CI integration job 的本地入口，断言 exit 1；非 staging 集群演练 |
| 日期 | 2026-11-08 |
| 环境 | `docker-compose.integration.yml` 全栈（MySQL 8.0.46 / Redis 7 / MinIO），`make test-integration` 对应的 `scripts/ci/run-integration-tests.sh` |
| 被测门禁 | 本次新增的 **S5-5 集成测试清单断言**（脚本前置执行，三层：包存在 / 关键用例点名 / 数量基线 ≥70） |
| 证据 | [artifacts/2026-11-08-s5-5/](./artifacts/2026-11-08-s5-5/)：baseline.log / drill1.log / drill2.log / restore.log |
| 结论 | **演练通过**：删单文件与删整包均确定性失败（exit 1），恢复后全量 9 包回归绿（75 个测试函数 ≥ 基线 70） |

## 1. 演练前状态（缺口复现）

CI `integration` job（`.github/workflows/ci.yml` → `run-integration-tests.sh` → `go test ./tests/integration/...`）对「测试被删」的天然结果是跑得更少、照样 PASS：删除 `tests/integration/iam/mysql_repository_test.go`（含 SQL 租户隔离、乐观锁、唯一约束、主体保护、审计同事务等 12 个用例）不会让任何 CI 步骤变红。该缺口正是 S5-5 退出条件要堵的对象。

## 2. 实测数据

| 步骤 | 操作 | 结果 | 证据 |
|---|---|---|---|
| 0 基线 | 不动测试树，全量运行 | **exit 0**，9 包全绿，48s；清单断言输出 9 包 OK / 15 个关键用例 OK / 75 ≥ 70 | baseline.log |
| 1 删单文件 | `mv tests/integration/iam/mysql_repository_test.go /tmp` | **exit 1**：3 个关键用例缺失（TenantIsolation / OptimisticLock / UniqueCode）+ 数量 63 < 70，四条 ERROR，未进入 compose / go test（快速失败） | drill1.log |
| 2 删整包 | `mv tests/integration/smoke /tmp` | **exit 1**：包级断言（smoke 无 \*_test.go）+ 用例级（TestSmokeMySQLMigration 缺失）双触发 | drill2.log |
| 3 恢复 | 文件移回原位，`git status` 干净 | **exit 0**：9 包全绿，75 个测试函数 | restore.log |

| 计时口径 | 结果 | 说明 |
|---|---|---|
| 检测用时 | **< 1s** | 清单断言为静态文本检查，在 compose up 之前执行；被删的测试在 CI 拉起容器前即被拦截 |
| 确认用时 | **N/A** | 门禁失败即确证，无人工确认环节 |
| 缓解用时 | **N/A** | 演练为 `mv` 移回即恢复，不涉及事故缓解 |

## 3. 门禁设计要点（scripts/ci/run-integration-tests.sh）

- **包级**：`REQUIRED_INTEGRATION_PKGS` 九包逐个检查仍有 `*_test.go`——堵整包删除；
- **用例级**：`REQUIRED_INTEGRATION_TESTS` 15 个关键场景点名存在（租户隔离、乐观锁、唯一约束、主体保护、审计同事务、复核扫描、令牌重放、断电不丢、保留期、冒烟迁移等）——堵「包还在、关键用例被删」；
- **数量级**：总函数数 ≥ `TEST_INTEGRATION_MIN`（默认 70）——堵「只删一半」的温和侵蚀；
- 全部为静态 grep 计数（确定性、无运行时推断），在 compose up 之前执行，失败即 exit 1，CI `integration` job 直接变红。

调整基线（新增/移除关键用例、修改 MIN）必须走 PR 并在演练记录或计划中说明理由，防止「顺手放宽基线」绕过本门禁。

## 4. 范围说明

- 本演练覆盖「本地入口 = CI integration job 同一脚本」；`.github/workflows/ci.yml` 的 job 本身在 CI 环境执行同一脚本，阻断语义一致（CI-16 parity 已映射 `[integration] → test-integration`）。
- 未演练：直接删 workflow job（由 `check-ci-parity.sh` 的 job↔make 目标映射兜底）与改阈值（TEST-05 / S5-5 前半已验证）。
