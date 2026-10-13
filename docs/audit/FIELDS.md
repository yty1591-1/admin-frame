# 审计字段字典（AUD-02）

与 `internal/shared/audit.Event` JSON / slog 键名对齐。

| 字段 | 类型 | 必填 | 说明 | 脱敏 |
|------|------|------|------|------|
| `event_id` | string | 是 | 事件唯一 ID（UUID 或等价） | — |
| `action` | string | 是 | 见 EVENTS.md | — |
| `actor_id` | string | 是 | 操作者 subject；匿名失败可用 `anonymous` | 禁止 Token |
| `actor_type` | string | 否 | `user` / `staff` / `system` / `service` | — |
| `channel` | string | 是 | `admin` / `biz` / `c` / `worker` | — |
| `merchant_id` | string | 条件 | B 端或商户域操作必填 | — |
| `resource_type` | string | 是 | 如 `session`、`role`、`order`、`export` | — |
| `resource_id` | string | 是 | 资源 ID；无则用 `-` | — |
| `outcome` | string | 是 | `success` / `failure` / `denied` | — |
| `occurred_at` | RFC3339 | 是 | UTC 发生时间 | — |
| `trace_id` | string | 是 | 与请求追踪对齐；无则 `-` | — |
| `service` | string | 是 | 如 `admin-api` | — |
| `reason` | string | 否 | 失败/拒绝原因（笼统，无内部栈） | 禁 SQL/DSN |
| `attrs` | object | 否 | 扩展属性（导出行数、过滤条件哈希等） | 禁 PII 原文 |
| `audit` | bool | 是* | 日志通道标记，恒为 `true`（SlogEmitter） | — |

\* 经独立审计表/流存储时可不写 `audit` 键，但查询侧须能区分审计与普通日志。
