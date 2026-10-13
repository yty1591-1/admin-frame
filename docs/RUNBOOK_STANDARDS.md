# 工程化 Runbook 与值班规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库告警关联的处置手册、值班与升级路径  
> **关联**：架构 §11.4 / §18.5；可观测见 `OBSERVABILITY_STANDARDS.md`；回滚见 `RELEASE_STANDARDS.md`  
> **执行方式**：`check-runbooks.sh` + 告警目录 + Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 `docs/runbooks/`、`scripts/ci/check-runbooks.sh` 与架构 §18.5，经架构 Owner / SRE Review

---

## 1. 目标

1. **告警可处置**：P1/P2 告警必须能跳到具体 runbook，而非口头经验。  
2. **升级路径清晰**：值班不知所措时知道找谁、何时升级。  
3. **演练可沉淀**：处置结果回写 runbook，避免重复踩坑。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| RB-01 | 所有 runbook 位于 `docs/runbooks/`；命名 `kebab-case.md` | `check-runbooks.sh` |
| RB-02 | 正文必须含规定章节：症状、影响、排查、缓解/恢复、升级、相关链接（见 §3） | `check-runbooks.sh` |
| RB-03 | 维护 [`docs/runbooks/ALERTS.md`](./runbooks/ALERTS.md) 告警目录：每个 **P1/P2** 条目必须有 runbook 相对链接 | `check-runbooks.sh` |
| RB-04 | 仓库至少具备基线 runbook：`api-5xx-spike`、`db-connection-pool`、`release-rollback` | `check-runbooks.sh` |
| RB-05 | 新增生产告警规则的 MR **必须**同步更新 `ALERTS.md` 与对应 runbook（可新建） | Review + PR 模板 |
| RB-06 | 文档化值班与升级路径（见 §5）；联系方式可用职位/值班表链接，禁止只写私人手机散落各处 | Review |
| RB-07 | 合并请求触及告警/runbook 时须勾选自检 | PR 模板 |

---

## 3. Runbook 必备章节

新建须复制 [`docs/runbooks/_TEMPLATE.md`](./runbooks/_TEMPLATE.md)，并保留以下二级标题（允许中英文，但 CI 检查中文标题关键字）：

| 章节 | 用途 |
|------|------|
| `## 症状` | 何时触发 / 表象 |
| `## 影响` | 用户与端（A/B/C）范围 |
| `## 排查` | 有序检查项（链接仪表盘、日志查询示例） |
| `## 缓解与恢复` | 可执行步骤；含回滚或限流等 |
| `## 升级` | 何时升级、升级给谁 |
| `## 相关链接` | 告警名、仪表盘、ADR、专文 |

`_TEMPLATE.md` 与 `README.md`、`ALERTS.md` 不要求完整六章（目录/模板豁免）。

---

## 4. 告警级别

| 级别 | 含义 | 响应期望 |
|------|------|----------|
| P1 | 核心链路不可用或严重数据风险 | 立即响应；按 runbook；必要时升级 |
| P2 | 显著降级或单端故障 | 值班窗口内处置 |
| P3 | 需关注的趋势 / 非紧急 | 工作时间处理；仍建议有 runbook |

**P1/P2** 在 `ALERTS.md` 中为必填 runbook 链接；P3 鼓励但不阻断。

---

## 5. 值班与升级（基线）

写入 `docs/runbooks/README.md` 或组织值班系统，并在 README 中给出入口链接：

1. **L1 值班**：确认告警 → 打开 runbook → 执行缓解。  
2. **L2（端/域 Owner）**：L1 在 runbook「升级」时限内未恢复，或涉及数据修复。  
3. **L3（架构 / SRE Oncall）**：多端同时故障、数据丢失风险、安全事件。  

升级时限建议：P1 在 **15 分钟** 内无收敛迹象即升级；P2 在 **60 分钟** 内。组织可 ADR 调整，但不得删除升级章节。

---

## 6. MUST NOT

- 新增 P1/P2 告警却无 runbook、无 `ALERTS.md` 条目。  
- Runbook 只有「联系某某」而无排查步骤。  
- 在 runbook 中写入生产密码、私钥、完整连接串。  
- 用过期的个人聊天记录替代正式 runbook。  

---

## 7. 本地与 CI

```bash
make check-runbooks   # RB-01～04
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-runbooks.sh` |
| MR | PR 模板「Runbook 自检」 |

---

## 8. 例外

1. **外部监控系统已有 runbook URL**：`ALERTS.md` 可链到外部，但仓库内仍须有摘要或跳转页，避免单点在外部消失。  
2. **演练新建草稿**：可用 `docs/runbooks/drafts/`，不纳入 RB-04；转正时移入根目录并登记告警。  
3. **紧急告警先上后补文档**：Owner 批准；**24h 内**补齐 runbook 与目录。

---

## 9. Review 清单

- [ ] 新告警已写入 `ALERTS.md` 且级别正确  
- [ ] 对应 runbook 六章齐全、步骤可执行  
- [ ] 无密钥；升级路径有效  
- [ ] `make check-runbooks` 通过  

---

## 10. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `docs/runbooks/_TEMPLATE.md` | 模板 |
| `docs/runbooks/ALERTS.md` | 告警 ↔ runbook 目录 |
| `docs/runbooks/*.md` | 处置手册 |
| `scripts/ci/check-runbooks.sh` | RB-01～04 |
| 本文件 | 规范正文 |
