# 发布回滚

| 字段 | 内容 |
|------|------|
| 告警 ID | `release.health_failed` |
| 级别 | P1 |
| 服务 / 端 | 按发版影响端 |
| 最后更新 | 2026-10-13 |

## 症状

- 发版后 `/readyz` 失败、5xx 激增、关键业务冒烟失败。
- 变更窗口内用户投诉与监控同时告警。

## 影响

- 受影响端流量错误或不可用。
- 若已执行 contract 类迁移，回滚应用**不等于**可安全 down 迁移。

## 排查

1. 确认发布单：镜像 **tag + digest**、变更端、是否含迁移。
2. 对比故障实例 `/version` 与上一良好版本。
3. 区分：应用 bug / 配置错误 / 迁移不兼容 / 依赖故障。

## 缓解与恢复

1. **应用回滚**：Deployment 切回上一已知良好 **digest**（见 RELS-04）。
2. 等待新副本 Ready；确认 `/healthz`、`/readyz`、`/version`。
3. **迁移**：仅 expand 且旧代码兼容时可保留；已 contract 或不可逆 → 按 DATA / ADR，禁止盲目 `migrate down`。
4. 业务冒烟：登录、关键读写、C 端下单幂等路径。
5. 冻结继续发版直至根因明确。

## 升级

- 回滚后仍失败 → L2 Owner + L3 SRE；涉及数据 → DBA。
- 安全相关发版问题同步安全值班。

## 相关链接

- [ALERTS.md](./ALERTS.md)
- [RELEASE_STANDARDS.md](../RELEASE_STANDARDS.md)
- [DATA_STANDARDS.md](../DATA_STANDARDS.md)
- [docs/release/CHECKLIST.md](../release/CHECKLIST.md)
