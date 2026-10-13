# \<事故或告警短名\>

| 字段 | 内容 |
|------|------|
| 告警 ID | （与 ALERTS.md 一致） |
| 级别 | P1 / P2 / P3 |
| 服务 / 端 | admin-api / biz-api / c-api / worker / DB |
| 最后更新 | YYYY-MM-DD |

## 症状

- 触发条件：
- 用户可见表现：

## 影响

- 影响端与范围：
- 数据正确性风险：有 / 无

## 排查

1. 打开仪表盘：…
2. 查日志（注意脱敏）：`service=… level=error`
3. 检查 `/readyz`、依赖与近期发布：

## 缓解与恢复

1. …
2. 回滚参见 [release-rollback.md](./release-rollback.md)（如适用）
3. 恢复后确认指标回落与冒烟通过

## 升级

- P1：15 分钟内无收敛 → L2 Owner → 必要时 L3 SRE
- 升级联系：见 [README.md](./README.md) 值班入口

## 相关链接

- 告警目录：[ALERTS.md](./ALERTS.md)
- 专文：…
