# 工程化注释规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部 Go 代码（含 `pkg/`、`internal/`、`cmd/`）  
> **执行方式**：人工 Review + `golangci-lint`（`revive` exported / package-comments、`nolintlint`）门禁  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新架构文档对应小节与 `.golangci.yml`，经架构 Owner Review

---

## 1. 目标

1. **可检索**：`go doc` / IDE 悬停即可理解导出 API，无需翻实现。
2. **可审查**：注释携带不变量、边界与安全约束，降低误用。
3. **可强制**：规范可被 linter 检查；例外必须 `nolint` 并写明原因。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| C-01 | 每个非 `main`、非 `_test` 包必须有**包注释**（`package` 语句正上方或独立 `doc.go`） | revive `package-comments` |
| C-02 | 每个**导出**的类型、函数、方法、变量、常量、接口必须有 godoc 注释 | revive `exported` |
| C-03 | 导出注释**必须以声明名称开头**（中英均可，名称须原样出现） | Review + revive |
| C-04 | `pkg/` 下导出 API 注释必须完整到「可独立作为外部库使用」的程度（行为、错误、并发、零值） | Review |
| C-05 | 禁止无意义注释（复述标识符、翻译变量名、叙述「开始/结束」） | Review |
| C-06 | `//nolint` / `//nolint:<linter>` **必须**写具体 linter 名 + 简短原因 | nolintlint |
| C-07 | 忽略 error（`_ =`）必须在同行或上一行注释说明**为何可忽略** | Review |
| C-08 | 安全/隐私相关行为（脱敏、Token、PII、权限）必须在注释中标明约束 | Review |
| C-09 | 领域不变量、状态机合法迁移、幂等语义写在类型或方法旁，不得只藏在 PR 描述里 | Review |
| C-10 | 注释与代码不一致视为缺陷；改行为必须改注释（同 MR） | Review |

**测试文件**：`_test.go` 中的导出符号（如 `TestXxx` 以外的 Helper）若导出，同样适用 C-02；包测试可用 `package foo_test` 而无包注释要求（由 lint 配置排除）。

**`main` 包**：`cmd/*/main.go` 免包注释；但包内若另有导出符号，仍须注释（正常 `main` 不应导出）。

---

## 3. 写法标准

### 3.1 包注释

```go
// Package errors 提供生产级错误原语：语义码、因果链、调用栈与结构化字段。
//
// 本包不得依赖 internal/；业务数字错误码见 internal/shared/apperr。
package errors
```

或使用 `doc.go` 承载较长说明（推荐 `pkg/` 公共库）。

要求：

- 首句概括职责；必要时第二段写清**非职责**与依赖边界。
- 多文件包只在一处写包注释（通常 `doc.go`），禁止重复冲突。

### 3.2 导出声明

```go
// NotFound 返回 CodeNotFound 的错误，并在调用方捕获栈。
func NotFound(msg string) *Error { ... }

// Code 是稳定、机器可读的错误语义分类。
type Code string

// ErrNotFound 表示资源不存在的哨兵，可用 errors.Is 匹配。
var ErrNotFound = &Error{code: CodeNotFound}
```

要求：

- 首句：`名称 + 空格 + 描述`。
- 描述说明「做什么 / 不做什么」，而非复述名字。
- 返回 `error` 的函数：注明可返回的语义码或哨兵（或指向包级错误表）。
- 接受 `context.Context`：注明取消/超时如何影响行为（若非显然）。
- 非线程安全或要求调用方同步的类型：必须写明。

### 3.3 中英文

- 本仓库允许**中文** godoc；名称部分保持标识符原文。
- 句子完整；中文以 `。` 结尾，英文以 `.` 结尾（二选一，同一符号内勿混用）。
- 对外可能开源的 `pkg/` 优先中文清晰或中英并列，禁止无上下文的黑话缩写。

### 3.4 分组常量 / 变量

```go
const (
	// CodeNotFound 表示资源不存在。
	CodeNotFound Code = "not_found"
	// CodeInternal 表示内部错误，消息默认不对客户端暴露细节。
	CodeInternal Code = "internal"
)
```

每个导出常量/变量单独一行注释；禁止只在块顶写一句笼统说明代替逐项注释（块顶概述可有，但不能替代）。

### 3.5 接口

```go
// OrderRepository 订单聚合持久化端口。
type OrderRepository interface {
	// FindByID 按 ID 加载订单；不存在时返回 apperr.ErrNotFound。
	FindByID(ctx context.Context, id string) (*Order, error)
}
```

接口方法注释写清契约；实现侧仅在行为与接口注释有差异时补充。

---

## 4. 分层注释重点（SHOULD → 关键路径按 MUST 审查）

| 层 | 必须写清的内容 |
|----|----------------|
| `domain` | 不变量、状态迁移、哨兵错误含义 |
| `app` | 事务边界、幂等、跨聚合副作用 |
| `transport/*` | 端别差异、鉴权前提、DTO 字段裁剪、错误码映射 |
| `infrastructure` | 一致性、重试、投递语义（at-least-once 等） |
| `pkg/*` | 零值、并发、错误、性能注意点 |
| `internal/shared` | 跨域契约稳定性（错误码、Principal 字段） |
| `internal/platform` | 配置项含义、默认值、启动/关闭顺序 |

---

## 5. 明确禁止（MUST NOT）

```go
// 获取用户   // 禁止：复述函数名，无增量信息
func GetUser(...) 

// user id
var UserID string  // 禁止：无类型含义、无约束

// TODO fix later  // 禁止：无编号、无负责人、无到期的空 TODO（见下节）
```

- 禁止注释掉大段死代码提交（应删或用版本管理）。
- 禁止在注释中写入密钥、完整 Token、生产连接串。
- 禁止用注释代替本应存在的测试或类型约束（注释不能「保证」行为）。

---

## 6. TODO / FIXME / NOTE

| 标记 | 要求 |
|------|------|
| `TODO` | 必须含可追踪信息：`TODO(owner): 说明` 或 `TODO(#issue): 说明` |
| `FIXME` | 必须说明缺陷与风险；合并前原则上应清零，否则挂 issue |
| `NOTE` / `NB` | 仅用于非显然陷阱；勿滥标 |

空 `TODO`、无负责人且无 issue 的标记：**Review 可阻断**。

---

## 7. nolint 例外流程

```go
//nolint:revive // 第三方接口强制导出名与本地风格冲突
```

规则：

1. 必须指定 linter 名（禁止裸 `//nolint`）。
2. 必须有原因（`nolintlint`）。
3. 作用域尽量窄（行级优先于文件级）。
4. 新增文件级 nolint 需 Reviewer 明确同意。

---

## 8. Review / CI 检查清单

作者自检：

- [ ] 新增/修改的导出符号均有合格 godoc
- [ ] 新包有包注释或 `doc.go`
- [ ] 无复述型垃圾注释；行为变更已改注释
- [ ] `_ = err` / `nolint` 有理由
- [ ] `make lint` 通过

Reviewer 抽检：

- [ ] `pkg/` 与 `internal/shared` 注释是否达到可独立使用
- [ ] domain 不变量是否写在代码旁
- [ ] 是否存在注释与实现不一致

---

## 9. 与工具链的对应关系

| 工具 | 作用 |
|------|------|
| `.golangci.yml` → `revive`:`exported` / `package-comments` | 导出与包注释硬门禁 |
| `.golangci.yml` → `nolintlint` | 禁止无说明 nolint |
| `make lint` | 本地与 CI 统一入口 |
| 本文件 + `ARCHITECTURE.md` §8.6 | 规范正文 |

**非目标**：不强制英文句号（`godot`）——本仓库允许中文 `。` 结尾，避免误伤。

---

## 10. 示例对照

**不合格：**

```go
package pagination

func Parse(r *http.Request) (page, size int, err error) { ... }
```

**合格：**

```go
// Package pagination 提供 HTTP 分页/排序参数解析与默认值。
package pagination

// Parse 从请求解析 page/size；非法值返回 invalid_argument。
// size 默认 20，最大 100。
func Parse(r *http.Request) (page, size int, err error) { ... }
```
