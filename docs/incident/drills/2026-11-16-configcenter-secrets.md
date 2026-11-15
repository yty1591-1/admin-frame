# 配置中心 M2 密钥托管演练（本机 staging）

- 日期：2026-11-16
- 环境：本机 = staging（与 M1 约定一致）
- 结果：pass（单测 + 契约门禁 + 生命周期用例取证）

## 范围

覆盖 `AC-CC-14`～`17`：

1. 信封加密：LocalKEK seal/open、错误 AAD 失败、prod 拒绝 LocalKEK
2. 生命周期：创建 → 送审 → 双人审批 → 提交轮换/吊销/授权 → 再审批生效
3. 旁路：列表 / 详情仅指纹；无 grant resolve 拒绝
4. 自动生成：`platform.demo.hmac_key` / `auth.jwt.private_key` secret_rule + 启动接线

## 证据

| 项 | 证据 |
|----|------|
| AC-CC-14 | `pkg/envelope` 单测；`TestSealOpenRoundTrip` / `TestOpenRejectsWrongAAD` |
| AC-CC-15 | `TestSecretLifecycleAndResolve`：轮换/吊销/授权均需二次审批；待审期间 resolve 仍用旧版本 |
| AC-CC-16 | OpenAPI schema 无明文响应字段；biz-api warm 仅打指纹日志 |
| AC-CC-17 | `EnsureAutoSecrets` 已接入 `configcenterruntime` 启动播种 |
| 指标 | `config_secret_resolve_total` / `duration` / `plaintext_leak` |
| mTLS | `CONFIG_API_MTLS` + `secretruntime.LoadServerTLSFromEnv`；Helm NetworkPolicy |
| 压测 | 单机 resolve 目标对照 P99&lt;50ms；200 实例项按 M1 口径 Owner 放行外推 |

## 放行说明

- 200 实例压测：本机单机基准 + Owner 授权放行（同 M1 7 天 soak）
- 密钥样例文件（8 个 baseline）保留至 **M3** 再删除
- JWT 私钥已进入密钥子域生成；身份域运行时改读 secretclient 可随 M3 收口

## 后续

进入 M3：删除剩余密钥样例、CI 禁止 `configs/*.yaml`、KEK 轮换 / 恢复演练。
