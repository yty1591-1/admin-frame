## 变更说明

<!-- 做了什么、为什么 -->

## 影响端

- [ ] admin（A）
- [ ] biz（B）
- [ ] c（C）
- [ ] worker / migrate
- [ ] 仅文档 / CI

## 安全自检（SECURITY_STANDARDS）

- [ ] 无密钥、生产连接串、完整 Token 进入代码、配置样例或日志
- [ ] 新接口挂在正确端，且默认鉴权（健康检查白名单除外）
- [ ] B 端校验 `merchant_id`；C 端校验资源归属；响应已裁剪内部字段
- [ ] 无 SQL 拼接 / 命令注入；生产配置未使用 `auth.mode=demo`
- [ ] 未新增 pprof / Swagger（或已按 SEC-08 豁免并说明）

## API 自检（API_STANDARDS）

- [ ] 影响端的 `api/openapi/{admin,biz,c}.yaml` 已同步更新
- [ ] 统一信封 / 稳定错误码 / 分页（`page_size`≤100）符合规范
- [ ] 若有 breaking：对应端 `info.version` major 已提升，并写明弃用与迁移
- [ ] C 端写路径含 `Idempotency-Key`；无内部字段进入 C 契约
- [ ] 契约有变更时已附 `make openapi-changelog` 摘要，并通知契约消费方锁定/升级 tag 或 commit

## 测试自检（TESTING_STANDARDS）

- [ ] 触及的 domain 包有单测；核心 domain 覆盖率 ≥70%
- [ ] B 端租户隔离（`TestBizRoleTenantIsolation` / `TestBizChangeRequestTenantIsolation`）仍通过
- [ ] 未使用生产凭据或生产库跑测试

## 依赖自检（DEPENDENCY_STANDARDS）

- [ ] `go.sum` 已更新；无未审批 `replace`
- [ ] 新直接依赖已说明用途与许可证；`make vuln` / `make check-deps` 可通过

## 配置自检（CONFIG_STANDARDS）

- [ ] 仅更新 `configs/*.yaml.example`；未提交含真实密钥的本地 yaml
- [ ] 新配置项有默认值或 `Validate`；prod 未使用 `auth.mode=demo`

## 数据自检（DATA_STANDARDS）

- [ ] 迁移 up/down 成对；未在 API/worker 自动 migrate
- [ ] 发版顺序（expand/contract）已说明；不可逆已挂 ADR
- [ ] 未将手工改生产库作为常规手段

## 可靠性自检（RELIABILITY_STANDARDS）

- [ ] 新增外部调用带超时 context；无 `http.DefaultClient` / 无超时 Get/Post
- [ ] 写路径幂等明确；限流使用 `42900`
- [ ] 降级路径可观测（日志/指标）

## 可观测自检（OBSERVABILITY_STANDARDS）

- [ ] 日志含 `service`/`channel`/`env`/`trace_id`（请求路径）
- [ ] API 保留 `/healthz`、`/readyz`、`/version`
- [ ] A/B/C 关键写按 AUD 发出审计（见「审计自检」）

## Git 自检（GIT_STANDARDS）

- [ ] 经 MR 合入保护分支；分支名符合 `feature|fix|hotfix|chore|docs/*`
- [ ] Commit / MR 标题符合 Conventional Commits
- [ ] 未直推 `main`；未在描述中粘贴密钥

## 发布自检（RELEASE_STANDARDS）

- [ ] 若触及镜像/发版：无 `:latest`；`/version` 字段齐全
- [ ] 生产引用将使用 tag+digest；回滚点明确
- [ ] 发版将按 `docs/release/CHECKLIST.md` 执行（或本 MR 仅代码无关发版）

## Runbook 自检（RUNBOOK_STANDARDS）

- [ ] 若新增/变更 P1/P2 告警：已更新 `docs/runbooks/ALERTS.md` 与对应 runbook
- [ ] 未在 runbook 写入密钥或生产连接串

## 事故与值班自检（RUNBOOK_STANDARDS RB-08/RB-09）

- [ ] 若改变用户影响面：`docs/incident/SEVERITY_MATRIX.md` 的判定条件与时限已同步
- [ ] 若来自事故/演练：复盘用 `POSTMORTEM_TEMPLATE.md`，行动项已登记且带可执行验证方式
- [ ] 若触及呼叫链路：`bash scripts/ops/paging-probe.sh --labels severity=P1 --expect-receiver pager` 已跑（占位地址会失败）
- [ ] `make check-oncall` 通过

## 审计自检（AUDIT_STANDARDS）

- [ ] 新必审路径已登记 `docs/audit/EVENTS.md`，并经 `audit.Emitter` 发出
- [ ] 字段符合 `docs/audit/FIELDS.md`；无 Token/密码等进入审计载荷
- [ ] 保留期符合类别（认证≥180d / 资金导出≥365d）或已写 ADR

## 许可证自检（LICENSE_STANDARDS）

- [ ] 新直接依赖 SPDX 在允许列表；未引入禁止列表项
- [ ] 若调整 `deps/allowed-licenses.txt` / `forbidden`：已说明并获批

## 代码生成自检（CODEGEN_STANDARDS）

- [ ] 未手改带 `DO NOT EDIT` 的生成文件；变更来自再生
- [ ] 若新增生成路径：已更新 `docs/codegen/MANIFEST.md`

## 性能自检（PERFORMANCE_STANDARDS）

- [ ] 若触及已登记热点：已更新 `docs/perf/BASELINES.md` 或说明无回归动机
- [ ] 未移除分页上限 / 无界重试等背压手段

## 质量

- [ ] `make ci` 通过（含 arch 内 LIC/GEN/PERF 等 gates）
- [ ] 需要时已更新 ADR / OpenAPI / runbook
