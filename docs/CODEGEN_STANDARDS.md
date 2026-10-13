# 工程化代码生成规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：OpenAPI、sqlc、mockery 及其它 `go generate` 产物的目录、提交与修改策略  
> **关联**：API 见 `API_STANDARDS.md`；测试 mock 见 `TESTING_STANDARDS.md`；架构工具链  
> **执行方式**：[`docs/codegen/MANIFEST.md`](./codegen/MANIFEST.md) + `check-codegen.sh` + Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 MANIFEST、生成配置与 `scripts/ci/check-codegen.sh`，经架构 Owner Review

---

## 1. 目标

1. **源唯一体**：契约与 schema 只在约定源文件修改；生成物可复现。  
2. **禁手改**：带生成标记的文件禁止人工编辑业务逻辑。  
3. **可核对**：清单登记生成路径与命令；CI 可发现失控文件。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| GEN-01 | 维护 [`docs/codegen/MANIFEST.md`](./codegen/MANIFEST.md)：登记每类生成物的源、命令、输出路径、是否入库 | `check-codegen.sh` |
| GEN-02 | 生成的 Go 文件文首须含 `Code generated` 与 `DO NOT EDIT`（工具默认头即可） | `check-codegen.sh` |
| GEN-03 | **禁止**手改生成文件；变更须改源后重新生成并提交（若策略为入库） | Review + CI |
| GEN-04 | mockery 输出目录统一为 `internal/<domain>/mocks/`；配置见 `.mockery.yaml` | MANIFEST + Review |
| GEN-05 | sqlc 输出须在 MANIFEST 登记；SQL 源在 `migrations/` 或约定 `db/queries/`，禁止改 `*.sql.go` 手写业务 | MANIFEST + Review |
| GEN-06 | OpenAPI：`api/openapi/**` 为**手写源**（非生成物）；禁止从过时实现反向「只改 YAML 不改代码」长期漂移（API-02） | `make openapi` |
| GEN-07 | 合并请求触及生成物时须勾选自检 | PR 模板 |

---

## 3. 策略矩阵

| 产物 | 源 | 生成命令（示例） | 是否提交 | 手改 |
|------|----|------------------|----------|------|
| OpenAPI | `api/openapi/*.yaml` | （无） | **是（源）** | 允许改源；禁与实现长期不一致 |
| mockery | 接口定义 | `make mock` / `mockery` | **是** | **禁止** |
| sqlc | schema + queries | `sqlc generate` | **是** | **禁止** |
| 其它 `go:generate` | 标注处 | `go generate` | 按 MANIFEST | 按是否带生成头 |

未引入 sqlc/mockery 前，MANIFEST 可标 `status: reserved`；一旦添加输出文件，必须改为 `active` 并满足 GEN-02。

---

## 4. 目录与配置

```text
api/openapi/                 # 契约源（GEN-06）
internal/<domain>/mocks/     # mockery 输出
db/queries/                  # sqlc 查询源（引入后）
.mockery.yaml                # mockery 钉扎配置
docs/codegen/MANIFEST.md     # 清单（GEN-01）
```

新增生成器：先更新 MANIFEST 与本专文矩阵，再合入输出。

---

## 5. MUST NOT

- 在 `Code generated` / `DO NOT EDIT` 文件中修补业务逻辑。  
- 提交无 MANIFEST 登记的生成目录作为「临时拷贝」。  
- 删除生成头以绕过 GEN-02。  
- 将 OpenAPI 当作「生成后可丢」的产物而不入库。  

---

## 6. 本地与 CI

```bash
make check-codegen   # GEN-01/02
make mock            # 若已配置 mockery
make openapi         # 契约校验
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `arch` 内跑 `check-codegen.sh` |
| MR | PR 模板「代码生成自检」 |

---

## 7. 例外

1. **紧急热修生成物**：仅限生产事故；24h 内必须回到「改源 + 再生」并还原手改。  
2. **vendor 式第三方生成代码**：若必须修改，fork + replace（走 DEP）或包装适配层，不直接改 vendor。  
3. **实验分支**：可 gitignore 生成物，合入 main 前须按 MANIFEST 决策入库或 CI 再生。

---

## 8. Review 清单

- [ ] 若有生成文件 diff：来自再生而非手改  
- [ ] MANIFEST 已更新  
- [ ] 生成头完整；mock/sqlc 路径正确  
- [ ] `make check-codegen` 通过  

---

## 9. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `docs/codegen/MANIFEST.md` | GEN-01 |
| `.mockery.yaml` | mockery 约定 |
| `scripts/ci/check-codegen.sh` | GEN-01/02 |
| `make mock` | 本地再生 |
| 本文件 | 规范正文 |
