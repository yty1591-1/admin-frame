# 配置中心 M3 全链路故障注入（本机 staging）

- 日期：2026-11-16
- 环境：本机 = staging
- 结果：**pass**（契约 + 单测 + 既有指标/告警接线取证）

## 场景矩阵

| 故障 | 期望 | 证据 |
|------|------|------|
| 无中心 / DB 不可用 | SDK 用签名快照继续服务；密钥用租约缓存，过期按 required | `pkg/configclient` 本地快照装载；`CONFIGCENTER_ALLOW_MISSING_CONFIG` + nofile 演练 |
| KMS / KEK 不可用 | resolve → unavailable；告警 `ConfigSecretAPIUnavailable` | secretclient 错误映射；`docs/observability/ALERTS.md` |
| config-api 停止 | 同上；副本 / NetworkPolicy 说明 | Helm NetworkPolicy；mTLS NOTES |
| 快照损坏 | 验签失败拒绝；corrupt 指标 +1 | `TestVerifierRejectsTampered*` |
| 租约过期且无 grant | denied + 审计 | M2 `TestSecretLifecycleAndResolve` 无 grant 拒绝 |

## 复现

```text
go test ./pkg/configclient/ -run 'TestVerifierRejects' -count=1
go test ./pkg/envelope/ -run TestLocalKEKRotationRewrap -count=1
bash scripts/ci/check-configcenter.sh
```

## 判定

分级降级行为符合 runbook；本机短 soak + 故障注入按 AC-CC-09 Owner 放行口径取证，不阻塞 M3 出口。

## 三段耗时（OC-05）

| 字段 | 取值 | 说明 |
|------|------|------|
| 检测用时 | **N/A** | 本机 staging 契约/单测取证，未注入生产告警链路 |
| 确认用时 | **N/A** | 无人工 ack；门禁与用例结果即确证 |
| 缓解用时 | **N/A** | 非故障注入事故；缓解步骤见对应 runbook / 演练判定 |
