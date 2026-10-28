---
alwaysApply: true
scene: git_message
---
# 提交信息规范（生产级硬约束）

> 本文件是规范正文；`.cursor/rules/git-commit-message.mdc` 为其镜像，修改须同时提交两处，内容保持一致。
> 适用范围：本仓库全部提交（含 AI 生成、机器人提交）。
> 效力：与 `docs/GIT_STANDARDS.md` §4 同级；冲突时以 `commitlint.config.cjs` 的机器可校验项为准，并同步修订两者。
> 关联：`docs/GIT_STANDARDS.md`（GIT-05）、`commitlint.config.cjs`、`scripts/ci/check-git.sh`、PR 模板「Git 自检」。

---

## 1. 硬约束一览

| ID | 约束 | 校验方式 |
|----|------|----------|
| COM-01 | 必须为 Conventional Commits：`<type>(<scope>): <subject>` | commitlint |
| COM-02 | `type` 必须取自 §3 白名单，小写 | commitlint `type-enum` |
| COM-03 | `scope` 必须取自 §4 白名单或省略，小写 | Review |
| COM-04 | header ≤ 100 字符；建议 ≤ 72 | commitlint `header-max-length` |
| COM-05 | `subject` 不得为空、不得以句号结尾 | commitlint `subject-empty` + Review |
| COM-06 | 破坏性变更必须写 `!` **且**含 `BREAKING CHANGE:` 页脚 | Review |
| COM-07 | 一个提交只做一类变更；不相关变更必须拆分 | Review |
| COM-08 | 提交信息不得含密钥、Token、生产连接串、PII、内部地址 | Review + gitleaks |
| COM-09 | 不得使用 `update`、`wip`、`修改`、`提交` 等无信息量描述 | Review |
| COM-10 | 不得由 AI 编造需求编号、Issue 号、审批人 | Review |

---

## 2. 结构

```text
<type>(<scope>): <subject>          # 首行，必填
                                    # 空行，必填（有 body/footer 时）
<body>                              # 可选，说明「为什么」与「影响」
                                    # 空行
<footer>                            # 可选，BREAKING CHANGE / Refs / Closes 等
```

规则：

- 首行与 body 之间**必须**空一行，否则 `git log --oneline` 与解析工具会串行。
- 段落之间空一行，列表用 `- `。
- 全文使用 UTF-8，不混用 CRLF；不使用 emoji、制表符、尾随空白。
- 单条消息语言统一：默认中文；面向外部契约或纯英文模块可用英文；**同一段内不得中英混排**（`type`/`scope` 与专有名词除外）。

---

## 3. `type` 白名单与判定

| type | 使用场景 | 不适用 |
|------|----------|--------|
| `feat` | 新增对外可见的能力：端点、页面、配置项、状态机分支 | 仅内部结构调整 |
| `fix` | 修复缺陷、错误码、越权、数据不一致、panic | 为规避缺陷而重构 |
| `perf` | 外部行为不变的前提下改善时延、内存、查询、包体 | 附带行为变更 |
| `refactor` | 行为不变的结构调整：分层、命名、依赖收敛、装配简化 | 任何可观测行为变化 |
| `test` | 仅新增/修正测试、夹具、测试基座 | 测试中夹带生产代码修改 |
| `docs` | 仅文档、注释、注释中的契约说明 | 契约文件本身（走 `api`） |
| `style` | 仅格式化：空格、换行、import 排序 | 任何逻辑改动 |
| `build` | 构建系统、依赖版本、镜像、编译参数、代码生成产物 | CI 流水线定义 |
| `ci` | 流水线、门禁脚本、CI 检查项、actions/workflows | 业务脚本 |
| `chore` | 工程杂务：`.gitignore`、许可证、脚手架元数据、临时产物清理 | 有价值的功能或修复 |
| `revert` | 回滚既有提交（`revert: <原 header>` + `This reverts commit <sha>.`） | 手工重做 |

判定优先级（同一提交命中多项时取**影响最大**者，并在 body 说明）：

```text
安全修复/数据一致性 > feat > fix > perf > refactor > test > docs > build > ci > chore > style
```

补充：

- 修复安全缺陷用 `fix(security): …`；调整安全基线或密钥策略用 `chore(security): …`。
- 依赖升级：可运行行为不变用 `build(deps): …`；升级引入行为/契约变化须降级为 `fix`/`feat` 并在 body 写明。
- 移除能力属于破坏性变更，用 `feat!`/`refactor!` 并写 `BREAKING CHANGE:`，不得降级为 `chore`。

---

## 4. `scope` 白名单

| 分组 | 可用 scope |
|------|-----------|
| 后端域 | `identity`、`iam`、`platform`、`security`、`shared` |
| 通用包 | 包名本身（如 `db`、`redis`、`mq`、`outbox`、`httpx`、`jwt`、`storage`、`observability`）；跨多个包时用 `pkg` |
| 契约 | `api`、`openapi` |
| 交付 | `deploy`、`ci`、`scripts`、`migrations`、`deps`、`docs` |

规则：

- 小写、短横线分隔，不超过两级（禁止 `identity/app/service`）。
- 禁止使用 `misc`、`all`、`*`、`tmp`、人名、分支名、文件名作为 scope。
- 纯仓库级变更（许可证、README 顶层、格式化）可省略 scope，写成 `docs: …`。
- 一个提交原则上只出现一个 scope；跨域变更应拆分，确需合并时用更上层的 scope（`pkg`）或省略。

---

## 5. `subject` 写作规范

必须满足：

1. **动宾短语，动词开头**，中文用祈使式，不带时态助词。
2. 描述**变更结果**，不复述文件名、不罗列全部改动。
3. 末尾不加句号、不加感叹号；内部可用顿号、逗号、冒号。
4. 中文 ≤ 40 字，英文 ≤ 50 字符；header 总量 ≤ 100 字符（硬门禁），建议 ≤ 72。
5. 不含 Markdown 标记、issue 链接、代码块。

推荐动词（中文）：`新增`、`实现`、`补齐`、`修复`、`修正`、`移除`、`重构`、`优化`、`升级`、`迁移`、`拆分`、`合并`、`统一`、`对齐`、`启用`、`禁用`、`放宽`、`收紧`、`加固`、`替换`、`调整`、`登记`、`收敛`、`回滚`。

禁用词（作为唯一描述时）：`更新`、`修改`、`处理`、`完善`、`优化一下`、`相关`、`若干`、`小改动`。

示例：

```text
feat(identity): 新增 A 端 MFA 绑定与解绑接口
fix(iam): 修复跨商户查询未按 tenant 过滤导致越权
perf(iam): 批量审批改为单次事务写入
refactor(outbox): 抽出投递状态机并注入时钟
build(deps): 升级 grpc 至 v1.83.2
```

---

## 6. `body` 写作规范

写**为什么**与**影响**，不是复述 diff。必写 body 的场景：

- 破坏性变更（配合 `BREAKING CHANGE:`）
- 数据库迁移、数据回填、索引变更
- 配置项、环境变量、默认值变更
- 依赖升级（含安全升级）
- 契约变更（OpenAPI、生成产物）
- 安全相关修复、权限模型调整
- 性能优化（须给出基线/压测依据）
- 回滚（`revert`）

推荐结构（可裁剪）：

```text
- 背景：<触发问题 / 需求编号 / 事故号>
- 变更：<关键实现选择，1～3 条>
- 影响：<端点、配置、迁移、兼容性、灰度范围>
- 回滚：<回滚方式或“无需回滚”>
```

约束：

- 每行 ≤ 72 个半角字符（中文约 36 字），便于终端与 `git log` 阅读。
- 每条列表项聚焦一个事实，避免超过 3 行的长段。
- 不粘贴大段日志、堆栈、截图链接、内部 wiki 链接。

---

## 7. `footer` 写作规范

| Token | 用途 | 示例 |
|-------|------|------|
| `BREAKING CHANGE:` | 破坏性变更说明（必填且与 `!` 同时存在） | `BREAKING CHANGE: /v1/roles 响应移除 permissions 字段` |
| `Refs:` | 关联需求/任务/史诗编号 | `Refs: ENT-01` |
| `Closes:` / `Fixes:` | 关联 Issue，合入后自动关闭 | `Closes: #128` |
| `Co-authored-by:` | 真实协作者署名 | `Co-authored-by: Zhang San <zs@example.com>` |
| `Reviewed-by:` | 例外流程下的审批留痕 | `Reviewed-by: Li Si` |
| `Impact:` | 高风险变更的影响面（可选） | `Impact: 需先执行 012_add_index.up.sql` |
| `Rollback:` | 高风险变更的回滚步骤（可选） | `Rollback: 重新部署上一镜像，无需回滚迁移` |

约束：

- 编号必须来自真实来源（`git branch`、`docs/`、Issue）；无来源则省略，**禁止编造**。
- 不写 `Signed-off-by: AI` 或类似工具署名；仅在用户明确要求时添加 `Co-authored-by`。
- 一个 footer 块内同类 token 保持顺序：`BREAKING CHANGE` → `Impact` → `Rollback` → `Refs` → `Closes` → 协作者。

---

## 8. 破坏性变更

同时满足三条才算合规：

```text
feat(api)!: 收敛错误响应为统一信封

- 变更：错误体由 {code,msg} 改为 {code,message,trace_id}，删除 msg 字段
- 影响：所有调用方需同步升级 api-client >= 3.0.0

BREAKING CHANGE: 移除错误响应 msg 字段，改用 message
Refs: API-07
```

- `!` 放在 `type(scope)` 之后、`:` 之前。
- 必须写清迁移路径（谁、改什么、何时完成）。
- 必须同步更新 OpenAPI major、契约类型与 README 的升级说明。

---

## 9. 原子提交与拆分

- 一个提交只回答一个问题；能被一句话描述清楚的才是合格提交。
- 必须拆分的组合：功能 + 无关重构、格式化 + 逻辑变更、依赖升级 + 业务修改、多个不相关 scope、生成代码 + 与生成无关的手改。
- 允许合并的组合：实现 + 其对应测试 + 其对应文档片段（同一变更闭环），但需在同一 scope 内。
- 生成产物必须与源变更同提交（依据 `docs/CODEGEN_STANDARDS.md`），不得单独提交「重新生成」。
- 临时调试文件、覆盖率产物、本地可执行文件不得入库；提交前先 `git status` 复核。

---

## 10. 禁止清单（MUST NOT）

- `update` / `fix` / `wip` / `tmp` / `test` / `修改` / `更新代码` / `提交` 作为唯一描述。
- 大写 `type`、全大写 `scope`、中文 scope。
- 首行以句号、感叹号、emoji 结尾或包含 emoji。
- 一条提交混合 `feat` 与格式化/依赖升级等无关变更。
- 提交信息或代码中携带密钥、Token、Cookie、生产连接串、手机号/身份证等 PII。
- 编造 Issue 编号、需求编号、审批人、Release 版本。
- 通过 `--no-verify` 绕过 commitlint / pre-commit 门禁（例外须在 PR 说明并留痕）。

---

## 11. 正例 / 反例

| 反例 | 问题 | 正例 |
|------|------|------|
| `update` | 无 type、无信息量 | `chore(deps): 升级 sqlx 至 v1.4.0` |
| `fix bug` | 无 scope、无结果描述 | `fix(iam): 修复审批并发导致的重复扣减` |
| `feat: 修改了一些配置` | 模糊、混入无关变更 | `feat(config): 新增 outbox 重试上限配置项` |
| `feat(Identity): add MFA` | type 正确但 scope 大写、中英混排、无结果焦点 | `feat(identity): 新增 A 端 MFA 两步登录` |
| `refactor: 重构 + 修复分页` | 混合类型 | 拆为 `refactor(pagination): 抽出排序解析` 与 `fix(biz): 修复分页越界` |
| `feat: 完成 XXX 需求，改了 23 个文件` | 罗列过程、无影响说明 | `feat(identity): 完成 C 端短信登录`，body 写背景/影响/回滚 |

---

## 12. AI 生成流程（本规则对模型的执行要求）

1. **先取证**：只依据 `git diff --cached`、`git status`、分支名与用户明确给出的信息；信息不足时先询问或省略不确定部分，不得臆测。
2. **再分类**：按 §3 优先级判定唯一 `type`，按 §4 选择唯一 `scope`；不新造 type/scope。
3. **再拆分层**：若暂存区包含多个不相关变更，先给出拆分方案（分组文件 + 每组一条 message），由用户确认，不硬凑成一条。
4. **再写消息**：首行动宾短语；body 只写能从 diff 证实的内容（行为、契约、配置、迁移、兼容性），不虚构编号与背景。
5. **破坏性识别**：删除/重命名公开端点、字段、配置项，或修改默认语义 → 加 `!` 与 `BREAKING CHANGE:`，并写明迁移路径。
6. **安全检查**：diff 中出现疑似凭证、私钥、连接串、真实用户数据时，**拒绝生成提交信息**，先提示移除并清理历史。
7. **输出格式**：默认只输出一个可直接使用的提交信息代码块，不加解释、不加署名、不加工具标记；用户要求多候选时按推荐度排序并各附一句理由。
8. **多行提交**：提示用户使用 `git commit -F <file>` 或多行编辑，不用多次 `-m` 拼接，避免段落丢失。

---

## 13. 提交前自检

- [ ] 首行符合 `<type>(<scope>): <subject>`，type/scope 在白名单内
- [ ] header ≤ 100 字符，subject 动词开头、无句号、无 emoji
- [ ] 首行与 body 之间有空行；body 说明了背景/影响/回滚
- [ ] 破坏性变更带 `!` 与 `BREAKING CHANGE:`
- [ ] 编号真实可查；无密钥、Token、PII、内部地址
- [ ] 未混入无关变更、临时文件、生成产物残留
- [ ] `npx commitlint --from origin/main --to HEAD` 与 `make check-git` 通过
