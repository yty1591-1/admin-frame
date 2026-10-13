# 工程化数据与迁移规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库 schema 迁移、生产数据变更、备份与保留、多租户行级隔离相关 SQL  
> **关联**：架构 §十四；安全见 `SECURITY_STANDARDS.md`；发布顺序见 `RELEASE_STANDARDS.md`  
> **执行方式**：`check-migrations.sh` + Review + MR 模板  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 `migrations/`、`cmd/migrate`、`scripts/ci/check-migrations.sh` 与架构 §十四，经架构 Owner Review

---

## 1. 目标

1. **可回滚**：每次 schema 变更具备对称 down；不可逆须 ADR + 运维方案。  
2. **可审计**：禁止手工改生产库作为常规手段；变更经迁移与发布流水线。  
3. **可隔离**：B 端租户条件落在 SQL；备份与保留策略可执行、可演练。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| DATA-01 | 迁移文件命名：`migrations/<NNNNNN>_<desc>.up.sql` 与配对 `.down.sql`（六位序号） | `check-migrations.sh` |
| DATA-02 | 每个 `.up.sql` 必须有同名基线的 `.down.sql`；不可逆变更须 ADR 且 down 中显式失败并注明 ADR 编号 | `check-migrations.sh` + Review |
| DATA-03 | **禁止**在 `cmd/*-api`、`cmd/worker` 启动路径自动 migrate（含 ORM AutoMigrate）；仅 `cmd/migrate` 或发布流水线执行 | `check-migrations.sh` |
| DATA-04 | **禁止**将「手工改生产库 / 临时 SQL 直连」作为常规变更手段；应急须双人、工单与事后补迁移 | Review + PR 模板 |
| DATA-05 | 迁移与代码兼容顺序：expand → 发版兼容代码 → contract；破坏性删除须确认无流量后执行 | Review + MR 说明 |
| DATA-06 | 商户相关表须有 `merchant_id`（或等价）及索引；列表/更新/删除 SQL **必须**带租户条件（对齐 SEC） | Review + TEST-03 |
| DATA-07 | 生产库须有定期备份与保留策略（见 §5）；恢复演练至少每半年一次并记入 runbook | Review / SRE Checklist |
| DATA-08 | 合并请求触及迁移时须勾选数据自检 | PR 模板 |

---

## 3. 迁移目录与命名

| 约定 | 说明 |
|------|------|
| 目录 | 仓库根 `migrations/` |
| 序号 | 六位十进制，单调递增（如 `000001`、`000002`） |
| 描述 | 小写蛇形，简述变更（如 `create_orders`） |
| 成对 | `000002_add_orders_index.up.sql` ↔ `000002_add_orders_index.down.sql` |

示例：

```text
migrations/
  000001_create_orders.up.sql
  000001_create_orders.down.sql
```

一次 MR **优先**只改一个域的表结构；大表 / 锁表变更须评估在线 DDL 或分步发布，并在 MR 写明影响窗口。

---

## 4. 执行入口与发布顺序

### 4.1 唯一合法执行方

- `cmd/migrate`（本地 / CI / 发布 Job）  
- 或组织批准的发布流水线步骤（调用同一迁移器）

**禁止**：业务 API / worker 在 `main` 或 init 中调用 migrate / AutoMigrate。

### 4.2 expand / contract

1. **Expand**：加列 / 加表 / 新索引（旧代码仍可运行）。  
2. **发布**：部署兼容新旧 schema 的代码。  
3. **Contract**：删旧列 / 旧索引（确认无读取后）。  

回滚：先回滚应用版本，再按需 `migrate down`（须事先验证 down 在 staging 可执行）。

### 4.3 不可逆变更

若物理上无法安全 down（如销毁性脱敏、不可恢复的数据迁移）：

1. 提交 ADR（原因、影响、回滚替代方案、审批人）。  
2. `.down.sql` 必须以失败语句开头并注释 ADR，例如：

```sql
-- IRREVERSIBLE: ADR-00XX — do not run in production without Owner approval
DO $$ BEGIN RAISE EXCEPTION 'IRREVERSIBLE: ADR-00XX'; END $$;
```

（MySQL 可用等价的信号/过程；脚手架阶段允许 `SELECT 'IRREVERSIBLE: ADR-00XX';` 作为占位，接入真实引擎后改为引擎原生失败语句。）

---

## 5. 备份与保留（生产基线）

| 项 | 要求 |
|----|------|
| 全量备份 | 至少每日；保留 ≥ 7 天 |
| 增量 / WAL | 开启（若引擎支持）；保留与 RPO 目标对齐 |
| RPO / RTO | 写入 `docs/runbooks/` 对应库 runbook；未定义不得宣称「生产就绪」 |
| 恢复演练 | ≥ 每 6 个月一次；结果记入 runbook 变更记录 |
| 审计 / 导出数据 | 保留期遵循 `AUDIT_STANDARDS`（AUD-07）；不得仅依赖业务表软删 |

开发 / CI 库不适用本表，但**禁止**用生产备份还原到共享开发环境且未脱敏。

---

## 6. 模型与 SQL 纪律

- 领域模型 ≠ 表模型；转换仅在 infrastructure。  
- 主键策略、时间列（UTC）、软删 `deleted_at` 约定全库一致。  
- 禁止 `SELECT *`；列表必分页且有上限。  
- 禁止「先全表查出再在内存按商户过滤」作为唯一防护。  

---

## 7. MUST NOT

- 提交无配对 down 的 up。  
- 在 API/worker 启动时自动 migrate。  
- 生产库用客户端随意 `ALTER` / 改数据且不补迁移。  
- 无 ADR 的不可逆破坏性变更。  
- 将未脱敏生产数据带入开发机或 CI。  

---

## 8. 本地与 CI

```bash
make check-migrations   # DATA-01/02/03
make build-migrate
make migrate-up         # 本地脚手架 / 接入 DB 后执行
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-migrations.sh` |
| MR | PR 模板「数据自检」 |

---

## 9. 例外

1. **热修生产数据**：工单 + 双人 + 审计；24h 内补等价迁移或文档化一次性脚本哈希。  
2. **第三方托管自动备份**：可替代自建备份，但仍须满足保留与演练要求，并在 runbook 写明控制台路径。  
3. **紧急跳过 down 配对**：禁止；仅允许按 §4.3 不可逆路径。

---

## 10. Review 清单

- [ ] up/down 成对；命名符合 DATA-01  
- [ ] 无 API/worker 自动 migrate  
- [ ] 迁移与发版顺序已在 MR 写明（expand/contract）  
- [ ] 租户列 / 索引已覆盖（若触及商户表）  
- [ ] 不可逆已挂 ADR；备份/保留影响已评估  

---

## 11. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `migrations/*.sql` | schema 变更 |
| `cmd/migrate` | 唯一业务侧迁移入口 |
| `scripts/ci/check-migrations.sh` | DATA-01/02/03 |
| `docs/runbooks/` | 备份恢复与演练 |
| `docs/adr/` | 不可逆与分库演进 |
| 本文件 | 规范正文 |
