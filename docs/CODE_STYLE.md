# 工程化代码编写风格规范（硬约束）

> **状态**：生效中（Normative）  
> **效力**：与 `docs/ARCHITECTURE.md` 同等；违反则 MR **不得合并**  
> **适用范围**：本仓库全部 Go 代码（含 `pkg/`、`internal/`、`cmd/`）  
> **关联**：注释见 [`COMMENT_STANDARDS.md`](./COMMENT_STANDARDS.md)；分层与依赖见架构文档 §五～§六  
> **执行方式**：`gofmt` / `goimports` + `golangci-lint` + Code Review  
> **最后更新**：2026-10-13  
> **变更方式**：修改本规范须同步更新架构文档对应小节与 `.golangci.yml`，经架构 Owner Review

---

## 1. 目标

1. **一致**：任意模块读起来像同一个人写的，降低协作成本。
2. **可强制**：能靠格式化与 lint 拦住的，不靠口头约定。
3. **可审查**：风格例外必须显式、可追踪（`nolint` + 原因）。

---

## 2. 硬约束一览（MUST）

| ID | 约束 | 门禁 |
|----|------|------|
| S-01 | 提交前必须通过 `gofmt` 与 `goimports`（local-prefix 为本模块） | goimports / `make fmt` / `make lint` |
| S-02 | 命名遵循本文 §3；导出符号禁止无意义缩写与 stutter（如 `order.OrderService` 可接受，`order.OrderOrder` 禁止） | revive + Review |
| S-03 | 函数签名：`context.Context` 为第一个参数（若需要）；`error` 为最后一个返回值 | revive `context-as-argument` / `error-return` |
| S-04 | `context.Context` 的 key 类型必须是包内未导出自定义类型，禁止内建类型做 key | revive `context-keys-type` |
| S-05 | 错误处理采用早返回；禁止深层嵌套的 `if err == nil { ... }` 成功路径 | revive `indent-error-flow` |
| S-06 | 禁止点导入（`import . "..."`）；空白导入仅用于副作用且须注释 | revive `dot-imports` / `blank-imports` |
| S-07 | 接口定义在**消费侧**（或 `internal/shared` 契约）；实现放 infrastructure，禁止「为抽象而抽象」 | Review |
| S-08 | 禁止在 `pkg/`、domain、app 正常路径 `panic`；仅允许文档化的 `Must*` 或确属程序员错误 | Review |
| S-09 | 禁止导出返回未导出类型（泄漏实现） | revive `unexported-return` |
| S-10 | 忽略 error（`_ =`）必须注释原因；禁止裸 `_ = someFunc()` 无说明 | Review + errcheck |
| S-11 | 单文件、单函数保持可读：函数体建议 ≤80 行、圈复杂度建议 ≤15；超限须拆分或在 MR 说明 | Review（后续可启 cyclop） |
| S-12 | 并发：goroutine 必须可取消/可汇合；共享可变状态必须有同步；禁止请求路径裸飞 goroutine | Review（架构 §8.4） |

**测试文件**：表驱动测试优先；Helper 命名 `t.Helper()`；断言失败信息须可定位。

---

## 3. 命名（MUST）

| 元素 | 规范 | 正例 | 反例 |
|------|------|------|------|
| 包名 | 小写、简短、单数概念、无下划线；不与标准库抢名 | `order`、`apperr` | `OrderUtils`、`common` |
| 文件名 | 小写 + 下划线；测试 `_test.go` | `memory_repository.go` | `MemoryRepository.go` |
| 接口 | 名词或 `er`/`able`；单方法接口可用 `Xxxer` | `Repository`、`EventPublisher` | `IOrderRepo`、`OrderRepositoryInterface` |
| 实现 | 技术/存储前缀 + 角色，未导出时小写开头 | `MemoryRepository`、`mysqlOrderRepository` | `RepositoryImpl` |
| 应用服务 | `XxxService` | `OrderService` | `OrderManager`（除非确有 Manager 语义） |
| Handler | 包路径区分端 + `Handler` | `admin.Handler` | `AdminOrderHTTPHandlerV2` |
| Command/Query | 动词 + 名词 | `CreateOrderCommand` | `OrderCreate` |
| 事件 | 名词 + 过去式 | `OrderCreated` | `CreateOrderEvent` |
| 错误哨兵 | `Err` + 描述 | `ErrNotFound` | `NotFoundError`（类型可用 `AppError`） |
| 常量 |完整语义；相关常量成组 | `StatusPaid` | `S_PAID`、`STATUS_2` |
| 接收者 | 类型名 1～2 字母缩写，同一类型全文一致 | `func (s *OrderService)` | `func (this *OrderService)`、`func (self *...)` |
| 初始主义缩写 | 保持惯例大小写 | `ID`、`HTTP`、`URL`、`JSON` | `Id`、`Http`、`Url` |

transport 子包名允许 `admin` / `biz` / `c`（与 Channel 对齐）。

---

## 4. 格式与导入（MUST）

### 4.1 格式化

```bash
make fmt    # gofmt + goimports -local github.com/admin-frame/admin-frame
make lint   # 含 goimports 检查
```

- 禁止手调与 `gofmt` 不一致的对齐/换行。
- 一行长度无硬性字符上限，但禁止为「塞进一行」牺牲可读性；参数过多则换行并对齐。

### 4.2 导入分组

标准库 → 第三方 → 本模块（`github.com/admin-frame/admin-frame/...`），组间空行。由 `goimports` 保证。

```go
import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/pkg/errors"
)
```

- 别名仅在冲突或包名不清晰时使用（如 `admintransport`）。
- 禁止未使用导入（编译器已拦）。

---

## 5. 函数、控制流与错误（MUST）

### 5.1 签名

```go
// 正例
func (s *OrderService) CreateOrder(ctx context.Context, cmd CreateOrderCommand) (*domain.Order, error)

// 反例：error 不在最后；context 夹在中间
func Bad(id string, ctx context.Context, err error, n int) (error, int)
```

- 多个返回值时，业务结果在前，`error` 在最后。
- 需要取消/超时的 I/O 与用例入口**必须**带 `ctx`。

### 5.2 控制流

```go
// 正例：早返回
order, err := s.repo.FindByID(ctx, id)
if err != nil {
	return nil, err
}
return order, nil

// 反例：成功路径缩进过深
if err == nil {
	// ... 大段逻辑
}
```

- 优先卫语句；避免 `else` 包裹主逻辑（`superfluous-else`）。
- `switch` 优于长 `if-else` 链；状态机迁移集中在 domain 方法。

### 5.3 错误

- 业务/领域错误：`apperr` 或 `pkg/errors`，保留 `%w` / `Unwrap` 因果链。
- 禁止裸 `errors.New` / `fmt.Errorf` 散落业务（无包装、无码）——收口到 domain 哨兵或 `apperr`。
- 日志：预期内 4xx 用 Warn/Info；真正故障用 Error，并带 `channel` 等字段（不得打密钥/完整 Token）。

---

## 6. 类型、接口与包结构（MUST）

| 规则 | 说明 |
|------|------|
| 小接口 | 接口方法宜少；按需拆分 Reader/Writer |
| 消费方定义 | 接口放在使用方包，实现放 infrastructure |
| 结构体 | 导出字段须有 json/yaml 等 tag 时保持一致命名风格（`snake_case` 用于外部契约） |
| 零值 | 有用零值须在注释中说明；危险零值禁止「默默可用」 |
| 包循环 | 禁止；公共类型下沉 `internal/shared` 或 `pkg` |
| `init` | 业务包避免 `init`；仅允许注册类副作用且须注释 |

DTO / Command / domain 模型**分离**，禁止在 Handler 直接暴露聚合内部字段到 C 端。

---

## 7. 并发与资源（MUST）

- `go` 语句：有退出条件（`ctx.Done()` / channel 关闭 / `errgroup`）；请求内临时任务优先在当前调用栈完成，否则交 `cmd/worker`。
- `defer` 紧跟资源获取（`Unlock`、`Close`、`Cancel`）。
- 所有外部调用带超时 context。
- channel：明确所有权（谁关）；禁止向已关闭 channel 发送。
- `Mutex` 与数据零距离；锁粒度尽量小，禁止在持锁时做远程 I/O。

---

## 8. 测试风格（SHOULD → 关键路径按 MUST）

```go
func TestOrder_Cancel(t *testing.T) {
	tests := []struct {
		name    string
		status  domain.Status
		wantErr bool
	}{
		{name: "created ok", status: domain.StatusCreated, wantErr: false},
		{name: "paid conflict", status: domain.StatusPaid, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ...
		})
	}
}
```

- 表驱动 + `t.Run`；禁止依赖外部真实网络（用 fake/stub）。
- 测试包名：白盒 `package foo`，黑盒 `package foo_test`。
- 禁止测试间共享可变全局状态而不 Reset。

---

## 9. 明确禁止（MUST NOT）

| 禁止项 | 原因 |
|--------|------|
| `IOrderService` 式匈牙利命名 | 非 Go 习惯 |
| 随意 `goto` | 可读性崩溃 |
| 在库代码吞错并返回零值假装成功 | 掩盖故障 |
| 魔法数字散落 | 抽常量并命名 |
| 过深继承式「基类」 | Go 用组合 |
| 在 domain 引入 ORM/HTTP/Kafka SDK | 破坏分层 |
| 未审查的 `replace` 指向私人 fork | 供应链风险 |
| 提交生成物与手改混杂且无说明 | 难 review |

---

## 10. Review / CI 检查清单

作者自检：

- [ ] `make fmt && make lint && make test` 通过
- [ ] 命名符合 §3；签名符合 S-03
- [ ] 无深层 `if err == nil`；错误已包装或使用 apperr
- [ ] 无请求路径裸 goroutine；锁内无远程 I/O
- [ ] 新增接口在消费侧；实现可替换

Reviewer 抽检：

- [ ] 是否为抽象而抽象 / 是否泄漏未导出类型
- [ ] C 端 DTO 是否裁剪内部字段
- [ ] 复杂度过高是否应拆函数

---

## 11. 与工具链对应

| 工具 | 作用 |
|------|------|
| `gofmt` / `goimports` | S-01 格式与导入 |
| revive（见 `.golangci.yml`） | S-02～S-06、S-09 等 |
| errcheck / staticcheck / govet / unused | 正确性与死代码 |
| misspell / gocritic / whitespace / nakedret / usestdlibvars | 风格增强 |
| 本文件 + `ARCHITECTURE.md` §8.7 | 规范正文 |

注释类硬约束见 `COMMENT_STANDARDS.md`（C-01～C-10），二者同时满足方可合并。

---

## 12. 示例对照

**不合格：**

```go
func Get(id string, ctx context.Context) (error, *Order) {
	o, e := repo.Find(id)
	if e == nil {
		if o.Status != "paid" {
			return nil, o
		} else {
			return fmt.Errorf("bad"), nil
		}
	}
	return e, nil
}
```

**合格：**

```go
func (s *OrderService) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	order, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return order, nil
}
```
