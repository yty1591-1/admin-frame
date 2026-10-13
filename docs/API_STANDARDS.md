# 工程化 API 契约规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部对外 HTTP API（A/B/C）及对应 OpenAPI 契约  
> **关联**：架构 §十六；安全见 `SECURITY_STANDARDS.md`；CI 见 `CI_STANDARDS.md`  
> **执行方式**：oasdiff validate + oasdiff breaking + MR 模板 + Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新 `api/openapi/**`、`scripts/ci/check-openapi.sh`、流水线与架构 §十六，经架构 Owner Review

---

## 1. 目标

1. **契约优先**：公开接口以分端 OpenAPI 为准，实现与契约同 MR 更新。
2. **兼容可预期**：非 major 变更保持向后兼容；破坏性变更可检测、可阻断。
3. **跨端一致形状**：统一信封、稳定业务错误码、分页上限；字段按端裁剪。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| API-01 | 三端独立契约：`api/openapi/admin.yaml`、`biz.yaml`、`c.yaml`；共用模型仅放 `components.yaml` | `make openapi` / CI job `openapi` |
| API-02 | 公开路径/方法/请求响应字段变更须同步更新对应端 OpenAPI；禁止「先上线后补契约」 | Review + openapi job |
| API-03 | 业务响应使用统一信封：`code` / `message` / `data` / `trace_id`（`internal/shared/httpx`） | Review + 契约 schema |
| API-04 | 稳定业务错误码来自 `internal/shared/apperr`；已发布码禁止改语义；新码只追加 | Review + 单测 |
| API-05 | 列表强制分页查询参数 `page`、`page_size`；`page_size` 上限 **100**，超限返回 `40000` | 实现 + OpenAPI `maximum` |
| API-06 | URL 前缀：`/admin/v1`、`/biz/v1`、`/c/v1`；资源名词复数；动作用 HTTP Method | Review + 契约 paths |
| API-07 | 相对默认分支基线的 **breaking change** 阻断合并；例外仅当该端 `info.version` **major** 已提升 | oasdiff `--fail-on ERR` |
| API-08 | OpenAPI 必须通过语法/引用校验（oasdiff validate） | `make openapi` |
| API-09 | 弃用字段/路径须标 `deprecated: true`，并写 `x-deprecated-since`（ISO 日期）；最短保留 **一个 major 或 90 天**（取更长） | Review + oasdiff deprecation |
| API-10 | C 端契约禁止运营内部字段（如 `remark`、`cost_cent`）；B 端路径不得信任客户端伪造的跨商户范围 | Review（对齐 SEC-06/14） |
| API-11 | C 端创建类写操作请求头必须带 `Idempotency-Key` | 契约 `required` + Handler |
| API-12 | 合并请求须使用仓库 MR 模板并勾选 API 自检 | `.github/` 与 `.gitee/` PR 模板 |

---

## 3. 分端契约

| 端 | 文件 | 前缀 | 强调 |
|----|------|------|------|
| A | `api/openapi/admin.yaml` | `/admin/v1` | 可含运营字段；关键写/导出须可审计（产品与 AUD 专文） |
| B | `api/openapi/biz.yaml` | `/biz/v1` | 数据范围 = Token `merchant_id`；禁止跨商户列表 |
| C | `api/openapi/c.yaml` | `/c/v1` | 字段最小化；幂等；错误文案笼统 |

- 探针 `/healthz`、`/readyz`、`/version` 写入各端契约且 `security: []`。
- 共享信封、分页参数、标准错误响应只维护在 `api/openapi/components.yaml`，各端 `$ref` 引用。
- 错误码枚举与实现以 `internal/shared/apperr` 为准；契约中的 `ErrorCode` 须与之同步。

---

## 4. 信封、错误码与分页

### 4.1 信封

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "trace_id": "abc123"
}
```

- 成功：`code=0`，`message` 通常为 `ok`。
- 失败：非 0 业务码 + HTTP 状态映射（见 `httpx.FromError`）；禁止把堆栈、DSN、SQL 原文放入 `message`。

### 4.2 稳定业务码（基线）

| code | 含义 | 典型 HTTP |
|------|------|-----------|
| 0 | 成功 | 200 |
| 40000 | 参数非法 | 400 |
| 40100 | 未认证 | 401 |
| 40300 | 无权限 | 403 |
| 40400 | 不存在（含越权对外表现） | 404 |
| 40900 | 冲突 | 409 |
| 42900 | 限流 / 配额耗尽（REL-05） | 429 |
| 50000 | 内部错误 | 500 |

### 4.3 分页

- Query：`page`（默认 1）、`page_size`（默认 20，最大 100）。
- 列表 `data` 至少包含 `items` 与 `total`。
- 导出、大批量拉取走 A/B 专用任务，不得靠放大 `page_size` 绕过上限。

### 4.4 时间

- 对外时间字段使用 RFC3339（UTC）；契约中标 `format: date-time`。

---

## 5. 兼容、破坏性变更与弃用

### 5.1 兼容（非 major）

允许：新增可选字段、新增路径、新增枚举值、放宽校验（在安全允许范围内）、标记 `deprecated`。

### 5.2 破坏性（须 major）

例如：删除路径/字段/枚举值、必填新请求字段、收紧类型或上限、改变字段语义、重命名、将成功语义改为错误等。

流程：

1. 在契约中标记弃用（API-09），保留期内双写或兼容读取。  
2. 发布说明 / CHANGELOG 写明影响端与迁移方式。  
3. 提升对应端 OpenAPI `info.version` 的 **major**（semver，如 `1.2.0` → `2.0.0`）。  
4. CI：相对基线仍报告 breaking；**仅当 major 已提升时允许合入**（见脚本）。  
5. 到期删除前用指标或调用方白名单确认流量。

### 5.3 版本字段

- `info.version` 使用 semver（`MAJOR.MINOR.PATCH`）。  
- 路径级 `/v1` → `/v2` 用于无法在同前缀下平滑迁移的大改，与 `info.version` major 同步规划。

---

## 6. MUST NOT

- 三端共用一份「万能」OpenAPI，或在契约中混挂他端路径。  
- 未更新契约即合并公开 API 行为变更。  
- 复用已发布业务错误码表达新语义。  
- 在 C 端契约或 DTO 中暴露 `remark` / `cost_cent` 等内部字段。  
- 用 allowlist / ignore 文件长期屏蔽真实 breaking（临时豁免须 Owner + 到期日）。  
- 生产开启 Swagger UI 作为「契约浏览」替代品（见 SEC-08）。

---

## 7. 本地与 CI

```bash
make openapi          # API-01/07/08：validate + 相对基线 breaking
make ci               # 含 openapi
```

| 平台 | 入口 |
|------|------|
| GitHub Actions | job `openapi` |
| Gitee Go | step `openapi`（`gitee-job.sh openapi`） |
| MR | PR 模板「API 自检」 |

工具钉扎：oasdiff **v1.33.0**（`github.com/oasdiff/oasdiff`，见 `scripts/ci/check-openapi.sh`）。  
严重级别覆盖：`api/openapi/.oasdiff-severity.txt`。

基线引用解析顺序：`OPENAPI_BASE_REF` → `GITHUB_BASE_REF`（`origin/<name>`）→ `origin/main` → `origin/master` → `main` → `master`。  
浅克隆须 `fetch-depth: 0`（或等价取得基线对象）。无法解析基线时仅跑 validate，并打印告警（本地新仓库可接受；受保护分支 CI 应总能解析）。

---

## 8. 例外

1. **有意 breaking**：提升该端 `info.version` major，MR 说明迁移与弃用期；Owner Approve。  
2. **oasdiff 误报**：在 MR 说明规则 ID 与原因，经 Owner 同意后可于 `err-ignore` **临时**忽略并设到期日；禁止无到期日的全局忽略。  
3. **紧急热修**：绕过分支保护后 24h 内补跑 `make openapi` / `make ci` 全绿，并补契约。

---

## 9. Review 清单

- [ ] 影响端的 OpenAPI 已更新，路径前缀与权限正确  
- [ ] 信封与错误码未另起炉灶；新错误码已追加到 `apperr` 与 `components.yaml`  
- [ ] 列表含分页；`page_size` 上限 100  
- [ ] C 端无内部字段；写路径有 `Idempotency-Key`  
- [ ] 若存在 breaking：对应端 major 已提升，弃用与发布说明已写  
- [ ] `make openapi` 通过  

---

## 10. 与工具链对应

| 工具 / 文件 | 作用 |
|-------------|------|
| `api/openapi/{admin,biz,c}.yaml` | 分端契约 |
| `api/openapi/components.yaml` | 共享信封/分页/错误响应 |
| `api/openapi/.oasdiff-severity.txt` | 版本策略严重级别 |
| `scripts/ci/check-openapi.sh` | validate + breaking |
| `internal/shared/httpx` | 信封与分页解析 |
| `internal/shared/apperr` | 稳定业务码 |
| 本文件 | 规范正文 |
