# 配置中心 M3 KEK 轮换 / 恢复演练（本机 staging）

- 日期：2026-11-16
- 环境：本机 = staging（LocalKEK）；prod 步骤见 `docs/runbooks/key-ceremony.md`
- 结果：**pass**（CC-F-55）
- RPO：0（密文在 MySQL）
- RTO：≤ 30m（本机 rewrap 秒级；对齐计划表）

## 目的

验证新旧 `kek_key_id` 双钥 unwrap 窗口、存量密文 rewrap、旧 KEK 下线后无法解密。

## 本机步骤（LocalKEK）

1. 构造 `local/1` 与 `local/2` 两把 LocalKEK。
2. 用旧钥 Seal → 旧钥 Open 成功。
3. 明文用新钥 Seal（rewrap）→ 新钥 Open 成功。
4. 旧钥 Open 新密文失败（下线窗口验证）。

## 证据

| 项 | 载体 | 结果 |
|----|------|------|
| 双钥 rewrap | `pkg/envelope.TestLocalKEKRotationRewrap` | pass |
| prod 禁 LocalKEK | `TestRejectLocalKEKInProd` | pass |
| prod 清单 | `docs/runbooks/key-ceremony.md` Vault Transit 轮换 | 文档化 |

复现：

```text
go test ./pkg/envelope/ -run 'TestLocalKEKRotationRewrap|TestRejectLocalKEKInProd' -count=1
```

## 判定

- 本机 CC-F-55 达标；prod 必须以 Vault Transit 演练清单为准，禁止把 LocalKEK 带入 prod。

## 三段耗时（OC-05）

| 字段 | 取值 | 说明 |
|------|------|------|
| 检测用时 | **N/A** | 本机 staging 契约/单测取证，未注入生产告警链路 |
| 确认用时 | **N/A** | 无人工 ack；门禁与用例结果即确证 |
| 缓解用时 | **N/A** | 非故障注入事故；缓解步骤见对应 runbook / 演练判定 |
