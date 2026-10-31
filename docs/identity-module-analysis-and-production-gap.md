# 身份域模块分析与生产差距（基线索引）

> 状态：**分析结论已合并到修复日志与实施计划，本文件保留为基线索引，避免引用悬空。**
>
> - 逐项修复明细与验证证据：[identity-p0-p1-fix-log.md](./identity-p0-p1-fix-log.md)
> - 结构性改造实施计划与验收标准：[identity-p1-remaining-implementation-plan.md](./identity-p1-remaining-implementation-plan.md)
> - 剩余 P1 执行记录：[identity-p1-remaining-completion-report.md](./identity-p1-remaining-completion-report.md)
> - P1-1 执行报告：[identity-p1-1-argon2-execution-report.md](./identity-p1-1-argon2-execution-report.md)
> - SLO 与容量基线：[identity-slo.md](./identity-slo.md)

---

## 1. 问题编号索引

| 编号 | 问题摘要 | 状态 | 明细位置 |
|---|---|---|---|
| P0-1 | prod 未启用身份域、缺 hash key Secret | 已完成 | fix-log §2.1 |
| P0-2 | 管理员特权操作无二次认证 | 已完成 | fix-log §2.2 |
| P0-3 | Redis 故障导致全站 503 / readiness 摘流 | 已完成 | fix-log §2.3 |
| P0-4 | 清理吞吐不足、删除缺索引 | 已完成 | fix-log §2.4 |
| P0-5 | NetworkPolicy 未放行 OAuth/SMS 443 | 已完成 | fix-log §2.5 |
| P0-6 | 无身份域告警/仪表盘 | 已完成 | fix-log §2.6 |
| P1-1 | Argon2 在 DB 事务内 | 已完成 | 执行报告 |
| P1-2 | 每请求 2–3 次 Redis 往返 | 已完成 | completion-report §2 |
| P1-3 | 列表 `LIKE %kw%` + 全表 COUNT | 已完成（转义 + 计数上限） | fix-log §3.1 |
| P1-4 | 会话表增长无归档 | 已完成 | completion-report §3 |
| P1-5 | C 端换绑/注销无再认证 | 已完成 | fix-log §3.2 |
| P1-6 | PII 明文 subject / OAuth subject | 部分完成（S1–S3） | completion-report §4 |
| P1-7 | 审计覆盖缺口 | 持续补齐（读审计、MFA setup、短信、OAuth、限流） | fix-log §3.3 |
| P1-8 | 5xx 根因未记录 | 已完成 | fix-log §3.4 |
| P1-9 | SQL/C 端测试覆盖低 | 持续补齐 | fix-log §3.5 |
| P1-10 | 多租户唯一键缺失 | 已完成 | fix-log §3.6 |
| P1-11 | 管理端无限流/幂等 | 部分完成（处置路由强制幂等键） | fix-log §3.7 |
| P1-12 | 运行手册缺失 | 已完成 | fix-log §3.8 |
| P1-13 | 无 SLO/压测基线 | 部分完成（SLO 文档） | fix-log §3.9 |
| P1-14 | 权限粒度仅 read/write | 已完成 | completion-report §5 |

---

## 2. 使用约定

1. 新发现的问题先登记到 fix-log 的状态总览，再在对应章节补根因、修复与验证。
2. 本文不再重复明细，避免多份文档口径漂移；如需恢复完整分析正文，应从历史评审记录恢复，不得凭记忆补写。
3. 验收证据以 staging 的压测、迁移演练与回滚记录为准，代码内单测不等同于生产验收。
