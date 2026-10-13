# 工程化许可证与开源合规规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库主模块构建图中的直接/间接依赖许可证、第三方声明与引入审批  
> **关联**：架构 §8.5；依赖门禁见 `DEPENDENCY_STANDARDS.md`（DEP-05 执行本专文策略）  
> **执行方式**：`deps/allowed-licenses.txt` + `deps/forbidden-licenses.txt` + `go-licenses` + `check-license.sh` + Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 `deps/*`、`scripts/ci/check-license.sh`、DEP-05 引用与架构 §8.5，经架构 / 法务 Owner Review

---

## 1. 目标

1. **可阻断**：禁止类许可证不得进入主构建图。  
2. **可审批**：扩大允许列表须有理由与 Owner 批准。  
3. **可披露**：再分发时具备第三方声明入口（NOTICE）。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| LIC-01 | 允许列表维护于 [`deps/allowed-licenses.txt`](../deps/allowed-licenses.txt)；CI 用 `go-licenses check` 强制 | `check-deps.sh`（DEP-05） |
| LIC-02 | 禁止列表维护于 [`deps/forbidden-licenses.txt`](../deps/forbidden-licenses.txt)；不得与允许列表重叠 | `check-license.sh` |
| LIC-03 | 主构建图禁止：GPL-2.0/3.0、AGPL-3.0、SSPL、未知（unknown）及禁止列表中的其它项 | go-licenses + Review |
| LIC-04 | 新增**直接**依赖：MR 写明 SPDX 许可证、用途、是否传递传染义务；不在允许列表则先走 LIC-05 | Review + PR 模板 |
| LIC-05 | 调整允许/禁止列表须 Owner Approve，并在 MR 说明先例与风险；禁止「为过 CI 临时放行」无到期日 | Review |
| LIC-06 | 仓库维护 [`deps/NOTICE`](../deps/NOTICE)（或等价）说明第三方声明生成方式 | `check-license.sh` |
| LIC-07 | 合并请求触及依赖/许可证时勾选自检 | PR 模板 |

---

## 3. 允许与禁止（基线）

### 3.1 默认允许（与 go-licenses 名称对齐）

MIT、Apache-2.0、BSD-2-Clause、BSD-3-Clause、ISC、MPL-2.0、Unlicense、0BSD  

（以 `deps/allowed-licenses.txt` 为准，本表为摘要。）

### 3.2 默认禁止

见 `deps/forbidden-licenses.txt`。含传染性 copyleft、源码提供义务与主产品分发策略冲突的许可，以及无法识别的 `unknown`。

### 3.3 灰区（须 ADR）

| 类型 | 处理 |
|------|------|
| LGPL | 动态链接场景可能可接受；须 ADR + 法务意见后方可加入允许列表 |
| 双重许可 | 以实际选用的 SPDX 为准，并在 MR 固定版本 |
| 专有 / 商业 | 须采购合同与私有模块策略；不得 silently 进公开构建 |

---

## 4. 审批流程（扩大允许列表）

1. MR：附上游 SPDX / LICENSE 链接、依赖用途、替代方案。  
2. 说明是否影响再分发（容器镜像、SDK）。  
3. Owner（必要时法务）Approve。  
4. 更新 `allowed-licenses.txt` 与本专文摘要（若新增类别）。  
5. 跑通 `make check-deps` / `make check-license`。

缩减允许列表或扩大禁止列表：同样走 MR，评估现有依赖是否命中。

---

## 5. NOTICE 与再分发

- [`deps/NOTICE`](../deps/NOTICE) 说明如何生成第三方清单（推荐：`go-licenses report`）。  
- 对外发布二进制 / 镜像的流水线应附带或可下载 NOTICE（组织发布规范，见 RELS）。  
- 禁止删除上游要求保留的版权头（生成代码除外且须保留工具版权声明）。

---

## 6. MUST NOT

- 将 GPL/AGPL/SSPL/unknown 依赖合入主模块且无豁免 ADR。  
- 允许列表与禁止列表收录同一 SPDX 名。  
- 用 `replace` 指向未审计、许可证不明的 fork 以绕过检查。  
- 在 CI 中 `--ignore` 掉问题依赖且无到期日。  

---

## 7. 本地与 CI

```bash
make check-license   # LIC-02/06
make check-deps      # DEP-05 / LIC-01
make ci
```

| 平台 | 入口 |
|------|------|
| GitHub / Gitee | job `vuln`（deps）+ job `arch`（`check-license.sh`） |
| MR | PR 模板「许可证自检」 |

工具钉扎：`github.com/google/go-licenses@v1.6.0`（与 DEP 一致）。

---

## 8. 例外

1. **工具链 / 仅 CI 依赖**不进入运行时构建图：可不要求进入允许列表，但不得 `import` 进 `cmd`/`internal` 运行路径。  
2. **误判**：附 SPDX 证据后临时 allow，并开 issue 跟踪上游元数据修复。  
3. **紧急热修**：24h 内补齐许可证说明与检查全绿。

---

## 9. Review 清单

- [ ] 新直接依赖 SPDX 已核对且在允许列表  
- [ ] 未引入禁止列表项  
- [ ] 若改列表：有审批与说明  
- [ ] `make check-license` / `make check-deps` 通过  

---

## 10. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `deps/allowed-licenses.txt` | LIC-01 |
| `deps/forbidden-licenses.txt` | LIC-02/03 |
| `deps/NOTICE` | LIC-06 |
| `scripts/ci/check-license.sh` | 列表完整性与互斥 |
| `scripts/ci/check-deps.sh` | go-licenses 强制 |
| 本文件 | 策略正文 |
