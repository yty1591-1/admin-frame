# 配置中心 M3 签名密钥轮换 + AC-CC-10 灾备取证

- 日期：2026-11-16
- 环境：本机 = staging
- 结果：**pass**

## A. 快照签名密钥轮换

1. 发新钥 `k-new`，Verifier 同时持有 `k-old` / `k-new`。
2. 双钥窗口内新旧签名快照均可验签。
3. 吊销旧钥后，仅 `k-new` 可通过；旧签名 → `ErrSnapshotUnknownKeyID`。
4. 全量重签：用新钥重新 Sign 已发布快照并滚动下发。

证据：`pkg/configclient.TestVerifierDualKeyRotationWindow`。

```text
go test ./pkg/configclient/ -run TestVerifierDualKeyRotationWindow -count=1
```

## B. AC-CC-10 灾备（MySQL PITR + 配置/密钥表）

复用既有备份恢复证据链：

| 项 | 引用 |
|----|------|
| 备份 / PITR runbook | [`docs/runbooks/backup-restore.md`](../../runbooks/backup-restore.md) |
| Game Day G4 | [`docs/incident/GAME_DAY.md`](../GAME_DAY.md) |
| RPO / RTO 基线 | RPO≤5m（binlog 归档）；RTO≤60m（全备恢复） |

**M3 增补断言**（恢复后须核对）：

- `config_item` / `config_version` / `config_release` / `config_pointer` 行数与位点一致
- `config_secret` / `config_secret_version` / `config_secret_grant` / `config_secret_review` 存在且密文可被当前 KEK unwrap
- 签名快照可从对象存储 / 卷恢复并验签通过
- KEK 本身不在 MySQL 备份内（外部 Vault / 本机 env）

本机 staging 以单测 + runbook 清单 + Owner 放行记入 AC-CC-10；完整集群 PITR 按季度 G4 执行。

## 判定

签名轮换与灾备证据链满足 M3 出口；prod Vault KEK 轮换见 key-ceremony。

## 三段耗时（OC-05）

| 字段 | 取值 | 说明 |
|------|------|------|
| 检测用时 | **N/A** | 本机 staging 契约/单测取证，未注入生产告警链路 |
| 确认用时 | **N/A** | 无人工 ack；门禁与用例结果即确证 |
| 缓解用时 | **N/A** | 非故障注入事故；缓解步骤见对应 runbook / 演练判定 |
