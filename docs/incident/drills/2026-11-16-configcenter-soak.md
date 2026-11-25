# 配置中心 M1 本机 staging 演练（2026-11-16）

| 字段 | 内容 |
|------|------|
| 目的 | 关闭 `config-center-plan.md` §8.1 M1 剩余「需环境执行」项：双读零差异、发布→回滚、签名快照 / 无文件启动前置 |
| 环境 | **本机 staging**（`docker compose -f docker-compose.integration.yml` MySQL 8 + Redis 7；DSN `admin_frame_dev`） |
| 执行窗口 | 2026-11-16 |
| 参与角色 | 研发（执行）+ 项目负责人（授权） |
| 当前状态 | **已执行并通过**：`D1`～`D4` 通过；**7 天日历 soak 按负责人授权放行**；**T4 非密钥基线收缩已完成**（§7） |
| 关联 | [`runbooks/config-center.md`](../../runbooks/config-center.md)、[`config-center-plan.md`](../../config-center-plan.md) §5.8 / §8.1、`AC-CC-11` / `AC-CC-06` / `AC-CC-13` |
| 取证 | [`artifacts/2026-11-16-configcenter/evidence.json`](./artifacts/2026-11-16-configcenter/evidence.json)、[`platform.snapshot.json`](./artifacts/2026-11-16-configcenter/platform.snapshot.json) |

## 1. 覆盖的验收项

| 验收项 | 场景 | 判定载体 |
|--------|------|----------|
| `AC-CC-11` 双读零差异 | `D1` 播种后 / 发布对齐后 / 回滚后 | evidence.json `dual_read_*` |
| `AC-CC-06` 回滚 | `D3` 回滚到基线 Release | `rollback_release_id` + 双读归零 |
| `AC-CC-13` 签名快照 | `D4` Ed25519 签名落盘并验签 | `snapshot_verified=true` |
| 发布闭环 | `D2` 改 `log.level` → 双人审批 → 全量发布 | `published_release_id` |
| 7 天 soak | 日历等待 | **负责人授权放行**（见 §5） |

## 2. 前置检查

| # | 检查项 | 结论 |
|---|--------|------|
| 1 | MySQL / Redis 已起（integration compose） | 通过 |
| 2 | 迁移 `000030`～`000032` 已 up | 通过 |
| 3 | 演练脚本 `scripts/local/configcenter-drill` | 通过 |
| 4 | 集成测试 `CONFIGCENTER_TEST_MYSQL_DSN` | 通过（见 §4） |

## 3. 场景矩阵与结果

### D1 首次播种 + 双读零差异

| 项 | 内容 |
|----|------|
| 动作 | `EnsureBaseline(dev/platform)` 播种 54 键 |
| 判据 | `mismatch_count=0`、`missing_in_center=0`、`degraded=false` |
| 结果 | **通过**（`bootstrap_release_id=1042512709`） |

### D2 发布（仅 `strategy=all`）

| 项 | 内容 |
|----|------|
| 动作 | 改 `log.level=debug` → 含全量条目的 Release → submit → approve（异人）→ publish |
| 判据 | 指针切到新 Release；对齐基线后双读零差异 |
| 结果 | **通过**（`published_release_id=1042512713`） |

### D3 回滚

| 项 | 内容 |
|----|------|
| 动作 | Rollback 到播种基线快照（生成新 Release 指向历史） |
| 判据 | 生效指针变更；相对代码默认基线双读零差异 |
| 结果 | **通过**（`active_after_rollback=1042512719`） |

### D4 签名快照 / 无文件启动前置

| 项 | 内容 |
|----|------|
| 动作 | 导出生效快照 → Ed25519 签名 → `Verifier.Verify` |
| 判据 | 验签通过；快照不含密钥明文 |
| 结果 | **通过**（`snapshot_verified=true`） |
| 说明 | 完整「移除非密钥 yaml 后拉起四进程」依赖本机 `configs/dev/admin.yaml` 的 identity/S3 装配，留待 T4 删文件时再做进程级无文件启动；本轮以签名快照验签作为 AC-CC-13 前置取证 |

## 4. 附属门禁取证（同日）

```text
go test ./tests/integration/configcenter/ ./internal/configcenter/app/ \
  ./cmd/internal/snapshotconfig/ ./cmd/internal/configcenterruntime/ -count=1
# 全部 ok
```

## 5. 7 天 soak 授权放行

| 项 | 内容 |
|----|------|
| 原要求 | runbook「连续 7 天每日 `config_dual_read_mismatch=0`」 |
| 本轮决定 | **项目负责人 2026-11-16 授权**：本机即为 staging；日历 7 天等待直接通过 |
| 等价证据 | 单轮完整「播种 → 发布 → 回滚」三阶段双读均为零差异（evidence.json） |
| 残留风险 | 未覆盖多副本抖动与 7×24 采样中断；若后续上真实预发集群，建议补跑一轮日历 soak |

## 6. 复现命令

```powershell
docker compose -f docker-compose.integration.yml up -d mysql redis
$env:ADMIN_FRAME_DB_DRIVER='mysql'
$env:ADMIN_FRAME_DB_DSN='root:root@tcp(127.0.0.1:3306)/admin_frame_dev?parseTime=true&loc=UTC&charset=utf8mb4'
.\bin\migrate.exe -dir migrations -action up
go run ./scripts/local/configcenter-drill -out docs/incident/drills/artifacts/2026-11-16-configcenter
```

## 7. T4 非密钥基线收缩（同日追加）

| 项 | 内容 |
|----|------|
| 已删除（git） | 全部端样例 `admin/biz/c/worker.yaml.example`（dev/prod/staging）；`http` / `iam` / `log` / `staging` 模块样例 |
| 保留至 M2 | `env.example`（三环境）；`auth` / `identity` / `infra` / `notification` / `platform` 模块样例 |
| 门禁 | `check-no-static-config` / `check-idempotency` / `check-outbox` / `check-secret-provider` 已适配并通过 |
| 无文件启动 | `TestLoadWithoutConfigFile`（`cmd/internal/appconfig`）通过；签名快照验签见 §3 D4 |
| 本地 gitignore 文件 | `configs/dev/*.yaml` 仍可能存在于开发机（不入库）；切换全量配置中心时由操作者自行移除 |

## 8. 结论

M1 环境侧与基线收缩均已关闭；**可进入 M2（业务密钥托管）**。

## 三段耗时（OC-05）

| 字段 | 取值 | 说明 |
|------|------|------|
| 检测用时 | **N/A** | 本机 staging 契约/单测取证，未注入生产告警链路 |
| 确认用时 | **N/A** | 无人工 ack；门禁与用例结果即确证 |
| 缓解用时 | **N/A** | 非故障注入事故；缓解步骤见对应 runbook / 演练判定 |
