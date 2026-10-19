# Go 项目架构与工程规范

> **版本**：v2.12  
> **状态**：生效中（Normative）  
> **适用范围**：后端服务（A/B/C 三端业务；模块化单体 + 多可执行文件，可演进至微服务）  
> **读者**：后端工程师、技术负责人、Code Reviewer、SRE  
> **最后更新**：2026-10-13  
> **变更方式**：架构变更须提交 ADR（Architecture Decision Record），经评审合入后同步更新本文档  
> **关联硬约束**：  
> - [工程化注释规范](./COMMENT_STANDARDS.md)（C-01～C-10）  
> - [工程化代码编写风格规范](./CODE_STYLE.md)（S-01～S-12）  
> - [工程化 CI 流水线规范](./CI_STANDARDS.md)（CI-01～CI-14）  
>   - GitHub：[`.github/workflows/ci.yml`](../.github/workflows/ci.yml)  
>   - Gitee：[`.workflow/`](../.workflow/)（Gitee Go）  
> - [工程化安全规范](./SECURITY_STANDARDS.md)（SEC-01～SEC-14）  
> - [工程化 API 契约规范](./API_STANDARDS.md)（API-01～API-12）  
> - [工程化测试规范](./TESTING_STANDARDS.md)（TEST-01～TEST-08）  
> - [工程化依赖与供应链规范](./DEPENDENCY_STANDARDS.md)（DEP-01～DEP-08）  
> - [工程化配置与多环境规范](./CONFIG_STANDARDS.md)（CFG-01～CFG-08）  
> - [工程化数据与迁移规范](./DATA_STANDARDS.md)（DATA-01～DATA-08）  
> - [工程化可靠性与弹性规范](./RELIABILITY_STANDARDS.md)（REL-01～REL-08）  
> - [工程化可观测性规范](./OBSERVABILITY_STANDARDS.md)（OBS-01～OBS-08）  
> - [工程化 Git 协作规范](./GIT_STANDARDS.md)（GIT-01～GIT-08）  
> - [工程化发布与镜像规范](./RELEASE_STANDARDS.md)（RELS-01～RELS-08）  
> - [工程化 Runbook 与值班规范](./RUNBOOK_STANDARDS.md)（RB-01～RB-07）  
> - [工程化审计规范](./AUDIT_STANDARDS.md)（AUD-01～AUD-09）  
> - [工程化许可证与开源合规规范](./LICENSE_STANDARDS.md)（LIC-01～LIC-07）  
> - [工程化代码生成规范](./CODEGEN_STANDARDS.md)（GEN-01～GEN-07）  
> - [工程化性能与容量规范](./PERFORMANCE_STANDARDS.md)（PERF-01～PERF-07）  
> 经 `make ci` / 托管平台流水线与 Review 强制，违反不得合并。  
> **待落地硬约束专文**见 [standards-roadmap.md](./standards-roadmap.md)（当前 P0～P4 专文已全部落地；分支保护仍需管理员勾选）。

---

## 目录

1. [概述](#一概述)
2. [设计原则](#二设计原则)
3. [目录结构](#三目录结构)
4. [多端业务架构（A/B/C）](#四多端业务架构abc)
5. [分层职责](#五分层职责)
6. [依赖规则](#六依赖规则)
7. [请求生命周期与上下文](#七请求生命周期与上下文)
8. [代码规范](#八代码规范)
9. [一致性、事务与幂等](#九一致性事务与幂等)
10. [安全规范](#十安全规范)
11. [可靠性与弹性](#十一可靠性与弹性)
12. [可观测性](#十二可观测性)
13. [测试规范](#十三测试规范)
14. [数据与迁移](#十四数据与迁移)
15. [缓存与异步消息](#十五缓存与异步消息)
16. [API 规范](#十六api-规范)
17. [配置、密钥与多环境](#十七配置密钥与多环境)
18. [构建、发布与运维](#十八构建发布与运维)
19. [治理与演进](#十九治理与演进)
20. [架构合规 Checklist](#二十架构合规-checklist)
21. [附录](#附录)

---

## 一、概述

### 1.1 目标

本规范统一工程结构、分层方式、编码与运维约定，确保：

| 目标 | 含义 |
|---|---|
| **业务核心稳定** | 领域逻辑不随框架、数据库、协议变化而改动 |
| **模块边界清晰** | 多人协作无循环依赖与分层泄露 |
| **三端边界清晰** | A/B/C 身份、权限、数据范围互不串扰，可独立扩缩 |
| **可演进** | 从模块化单体平滑过渡到微服务，拆分成本可控 |
| **可观测、可测试** | 日志 / 指标 / 链路标准化，各层可独立测试 |
| **生产就绪** | 安全、弹性、发布、容量与事故响应有明确基线 |
| **可治理** | 决策可追溯（ADR），合入有门禁，责任归属清晰 |

### 1.2 架构总览

本项目采用 **多入口 + 共享内核**：三个 HTTP API 进程（及 worker / migrate）共享 `internal` 领域与基础设施，按端独立装配、独立部署。

```text
  Admin Web          Merchant Web         App / 小程序 / H5
       |                   |                      |
       v                   v                      v
  admin-api             biz-api                 c-api
  (cmd 装配)            (cmd 装配)              (cmd 装配)
       |                   |                      |
       +-------------------+----------------------+
                           | 只注册本端 transport
                           v
              transport/{admin,biz,c}     接入 / DTO / 路由
                           |
                           v
                         app              用例 / 事务 / 幂等
                           |
                           v
                       domain             聚合 / 规则 / 仓储接口
                           ^
                           | 实现接口
                   infrastructure         DB / MQ / 外部客户端
                           |
                       platform           配置 / 日志 / 指标 / 追踪

  cmd/worker  --> app（异步消费，不经 HTTP transport）
  cmd/migrate --> migrations（与业务进程解耦）
```

**依赖方向**：外层依赖内层；`domain` 不依赖外层；`infrastructure` 实现 `domain` 定义的接口（依赖倒置）。  
**端差异**只存在于 `cmd` 装配与 `transport`；**禁止**按端复制领域模型。

### 1.3 技术选型（推荐基线）

| 类别 | 选型 | 说明 |
|---|---|---|
| 语言 | Go 1.26+（推荐跟进当前 stable 补丁版） | `go.mod` 锁定 `toolchain` |
| Web 框架 | Gin / 标准库 `net/http` + mux | **禁止**在 domain 层引入 |
| RPC | gRPC + protobuf | 跨服务通信；REST 对外、gRPC 对内亦可 |
| 持久化 | `database/sql` + sqlc / 轻量封装 | 避免在 domain 暴露 ORM 模型 |
| 迁移 | golang-migrate / atlas | 可回滚；与业务发布解耦 |
| 配置 | 配置文件 + 环境变量（可用 viper） | 按 cmd 分文件；敏感项走密钥管理 |
| 日志 | `slog` / zap | 结构化 JSON；禁止打印密钥/PII 明文 |
| 可观测 | OpenTelemetry + Prometheus | Logs / Metrics / Traces；`service` 区分三端 |
| 测试 | testing + testify + testcontainers-go | 集成测试用真实依赖 |
| 工具链 | golangci-lint、goimports、mockery、govulncheck | CI 门禁强制 |
| 容器 | 多阶段 Dockerfile、多 target、非 root、固定 digest | 禁止 `latest` |

> 选型变更须走 ADR，并更新本节与相关运维文档。

### 1.4 非目标（明确不做）

- 在业务边界未清晰前，不做过度微服务拆分。
- 不在 domain 层引入“万能框架”或全局状态。
- 不通过「A/B/C 进程互相调 HTTP」完成内部协作（见第四章）。
- 不将 Web / App / 小程序前端工程结构纳入本文（后端契约边界为止）。
- 不将本规范当作唯一编码圣经：与本地既有设计冲突时，以 ADR 决议为准。

---

## 二、设计原则

1. **依赖单向向内**：`transport → app → domain ← infrastructure`；`platform` 作为横切能力由 `cmd` 注入。
2. **domain 层零外部依赖**：不出现 Gin、GORM、Redis、Kafka、HTTP 客户端等。
3. **接口定义在消费侧**：仓储 / 网关接口定义在 `domain`（或调用方 app），实现放在 `infrastructure`。
4. **cmd 层只做装配**：入口不含业务判断与 SQL；每个可执行文件只挂载本端路由与中间件。
5. **pkg 无业务语义**：出现 `Order`、`User` 等业务概念的包必须放回 `internal`。
6. **internal 禁止外部导入**：业务代码一律放在 `internal/`。
7. **DTO 与领域模型分离**：协议变化不污染领域层；**按端维护 DTO**。
8. **先模块化单体，后微服务**：边界未清晰前不过度拆分。
9. **失败默认安全**：鉴权失败拒绝、配置缺失拒绝启动、外部调用必超时。
10. **显式优于隐式**：事务边界、幂等键、超时、重试策略均应在代码与配置中可见。
11. **端隔离、域共享**：业务规则在 domain/app；端差异只在 transport 与 cmd 装配。
12. **禁止跨端身份冒用**：A/B/C 的 Token、issuer/audience、权限模型不得互通。

---

## 三、目录结构

### 3.1 根目录

```text
myproject/
├── cmd/                              # 每个子目录 = 一个可执行文件
│   ├── admin-api/main.go             # A 端：平台运营
│   ├── biz-api/main.go               # B 端：商家/企业
│   ├── c-api/main.go                 # C 端：消费者
│   ├── worker/main.go                # 异步任务 / 事件消费
│   └── migrate/main.go               # 数据库迁移
├── internal/
│   ├── platform/                     # 配置、日志、监控、追踪、健康检查
│   ├── shared/                       # 跨域共享契约（Principal、错误码等，无实现）
│   ├── <domain>/                     # 按业务域垂直切分（见 3.2）
│   └── ...
├── pkg/                              # 公共库，无业务语义
├── api/
│   ├── openapi/
│   │   ├── admin.yaml                # A 端契约
│   │   ├── biz.yaml                  # B 端契约
│   │   └── c.yaml                    # C 端契约
│   └── proto/                        # 按需
├── configs/
│   ├── admin.yaml.example
│   ├── biz.yaml.example
│   ├── c.yaml.example
│   └── worker.yaml.example
├── migrations/
├── deploy/                           # 三端可独立 Deployment / HPA
├── scripts/
├── tests/
│   ├── integration/
│   └── e2e/                          # 建议按端分子目录：admin/ biz/ c/
├── docs/
│   ├── ARCHITECTURE.md
│   ├── adr/
│   └── runbooks/
├── Dockerfile                        # 多 target：admin-api / biz-api / c-api / worker
├── Makefile
├── .golangci.yml
├── go.mod
└── go.sum
```

### 3.2 按领域拆分后的 internal

`internal` 按业务域垂直切分；**domain / app / infrastructure 三端共享**，接入层按端拆分：

```text
internal/
├── order/
│   ├── transport/
│   │   ├── admin/                    # A 端 Handler / DTO / router
│   │   │   ├── handler.go
│   │   │   ├── dto.go
│   │   │   └── router.go
│   │   ├── biz/                      # B 端
│   │   └── c/                        # C 端（本域无某端接口时可省略该目录）
│   ├── app/
│   │   ├── service.go
│   │   ├── command.go
│   │   └── event.go
│   ├── domain/
│   │   ├── aggregate/order.go
│   │   ├── valueobject/money.go
│   │   ├── service/pricing_service.go
│   │   ├── repository/order_repository.go
│   │   └── event/order_events.go
│   ├── infrastructure/
│   │   ├── persistence/order_repository.go
│   │   ├── cache/order_cache.go
│   │   └── client/payment_client.go
│   └── domain.go
├── merchant/
│   ├── transport/{admin,biz}/
│   ├── app/ | domain/ | infrastructure/
├── user/
│   ├── transport/{admin,biz,c}/
│   ├── app/ | domain/ | infrastructure/
└── payment/
    ├── transport/{admin,biz,c}/
    ├── app/ | domain/ | infrastructure/
```

> **简化模式**：领域逻辑较简单时可退化为少量文件，但仍须：**接口隔离、依赖注入、按端拆分 transport（若该域对多端暴露）**。

### 3.3 包边界判定

| 问题 | 放哪里 |
|---|---|
| 换一个项目还能直接复用？ | `pkg/` |
| 仅本服务多域共用的类型/错误码/Principal？ | `internal/shared/` |
| 某一域的业务规则？ | `internal/<domain>/domain/` |
| 某端的 HTTP DTO / Handler？ | `internal/<domain>/transport/<channel>/` |
| 对接某外部系统的适配？ | `internal/<domain>/infrastructure/` |

---

## 四、多端业务架构（A/B/C）

### 4.1 端定义

| 端 | Channel | 用户 | 典型能力 | 信任级别 | 限流 / 审计 |
|---|---|---|---|---|---|
| **A 平台运营** | `admin` | 内部员工 / 运营 | 全局配置、商户治理、风控、审计导出、权限管理 | 高（内网/VPN 优先） | 中等限流；**强审计**；写操作可要求 MFA |
| **B 商家/企业** | `biz` | 商户主体及其员工 | 店铺、商品、订单履约、商户内账号与角色 | 中（公网 + 商户认证） | 按商户限流；关键写审计 |
| **C 消费者** | `c` | 终端用户 | 浏览、下单、支付、个人中心 | 低（公网海量） | **强限流**；字段最小化；弱审计（登录/支付等关键点） |

### 4.2 可执行文件职责

| 进程 | 职责 |
|---|---|
| `cmd/admin-api` | 仅注册 `transport/*/admin` 路由与 A 端 AuthN/AuthZ；监听独立端口 |
| `cmd/biz-api` | 仅注册 `transport/*/biz`；强制商户上下文 |
| `cmd/c-api` | 仅注册 `transport/*/c`；强制用户归属与字段裁剪 |
| `cmd/worker` | 消费领域事件 / 定时任务；直接调用 app；不暴露业务 HTTP |
| `cmd/migrate` | 执行 `migrations/`；不与 API 进程同启自动跑 |

各 `*-api` **不得**编译或注册他端 Handler，避免误暴露与依赖膨胀。

### 4.3 代码放置规则

| 场景 | 放置 |
|---|---|
| 三端共用用例（如计价、库存扣减） | `app`，由各端 transport 调用 |
| 仅某端用例（如运营强制关店、C 端一键下单） | `app` 内独立用例方法；**仅**对应 cmd 注入并暴露 |
| DTO / Handler / 路由 | 仅本端 `transport/<channel>/` |
| 身份、错误码、分页等横切类型 | `internal/shared/` |

禁止：在 C 端 Handler 中 import A 端 DTO；禁止为「图省事」把三端 Handler 塞进同一 router 包再由三端 cmd 全量引用。

### 4.4 跨端协作

- **默认同进程协作**：共享 app/domain，或通过领域事件（worker 消费）最终一致。
- **禁止** `admin-api` HTTP 调用 `biz-api` / `c-api`（或反向）作为内部实现路径。
- 若未来拆成独立服务，协作改为同步 RPC（接口在消费侧）或事件总线，并另立 ADR。

### 4.5 数据范围（摘要）

| 端 | 数据范围 |
|---|---|
| A | 平台全局；操作须权限点 + 审计 |
| B | **强制** `merchant_id` 租户隔离（Token 内商户与路径/入参一致） |
| C | **强制**资源归属 `user_id`（或等价 subject）；禁止越权读写他人数据 |

细则见 [第十四章](#十四数据与迁移) 与 [第十章](#十安全规范)。

### 4.6 禁止事项

- C 端接口返回运营敏感字段（成本、内部备注、风控标签原文等）。
- B 端访问或修改其他商户数据（含「漏传 merchant_id 则查全库」）。
- A 端写操作绕过审计或权限点。
- 使用 A 端 Token 调用 B/C 接口（或任意跨端 Token 复用）。
- 三端共用同一 JWT `audience` / 签名密钥且不做 channel 校验。

---

## 五、分层职责

### 5.1 cmd —— 入口层

**职责**：加载**本进程**配置 → 校验 → 初始化平台能力 → 装配依赖 → **只注册本端路由** → 启动 → 优雅关闭。

**禁止**：业务判断、SQL、HTTP Handler 逻辑；注册他端 transport。

```go
func main() {
    cfg := platform.LoadConfig("admin")          // 1. 按 cmd 加载
    if err := cfg.Validate(); err != nil {
        log.Fatal(err)
    }
    logger := platform.NewLogger(cfg.Log)        // 2. 平台能力
    db := platform.NewDB(cfg.DB)                 // 3. 基础设施
    defer db.Close()

    repo := persistence.NewOrderRepository(db)   // 4. 装配
    svc := app.NewOrderService(repo)
    h := admintransport.NewOrderHandler(svc)     // 仅 A 端 transport
    r := admintransport.NewRouter(h, adminAuth)  // 5. 启动

    server := &http.Server{Addr: cfg.Addr, Handler: r}
    go func() {
        <-signalChan
        ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
        defer cancel()
        _ = server.Shutdown(ctx)
    }()
    if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        logger.Error("server stopped", "err", err)
        os.Exit(1)
    }
}
```

**生命周期要求**：

- 启动失败（配置非法、关键依赖不可达）必须非零退出。
- 收到 `SIGTERM`/`SIGINT` 后停止接新请求，排空进行中请求，关闭 DB/MQ 连接。
- 关闭超时可配置，默认建议 15–30s。

### 5.2 transport —— 接入层（按端）

**职责**：路由注册、**本端**鉴权中间件、参数解析与校验、DTO ↔ 命令对象转换、调用 app、响应封装与**按端字段裁剪**、错误码映射、请求日志。

**禁止**：直接访问 DB/MQ、编写业务规则、开启事务、调用第三方支付等副作用；引用他端 transport 包。

### 5.3 app —— 应用层

**职责**：用例编排、事务边界、跨仓储/跨领域协调、发布领域事件、幂等控制、**强制数据范围**（如写入 `merchant_id` / 校验 `user_id`）、返回用例结果。

**禁止**：依赖具体基础设施实现、拼接 SQL、渲染 HTTP 响应。

> 一个用例 = 一个公开方法。方法名使用动词短语：`CreateOrder`、`CancelOrder`、`HandlePaymentCallback`。

### 5.4 domain —— 领域层（核心）

**包含**：

- **聚合根**：唯一标识与生命周期，如 `Order`。
- **值对象**：由属性决定相等性，不可变，如 `Money`、`Address`。
- **领域服务**：跨聚合复杂逻辑，如 `PricingService`。
- **仓储接口**：如 `OrderRepository`。
- **领域事件**：如 `OrderCreated`、`OrderPaid`。
- **领域错误**：如 `ErrInsufficientStock`、`ErrOrderAlreadyPaid`。

**禁止**：框架 / DB / 缓存 / 网络依赖；避免贫血模型——行为应内聚在实体上；**不感知 Channel**（端差异不泄漏进领域规则，除非规则本身是业务不变量）。

### 5.5 infrastructure —— 基础设施层

**职责**：实现 domain 定义的接口；接入 MySQL、Redis、Kafka、OSS、第三方支付、gRPC 客户端。

**约束**：

- 只实现已定义接口，不偷偷扩展 domain 未声明的能力。
- DO（持久化模型）↔ Entity 转换在此层完成。
- 查询条件须支持 `merchant_id` / `user_id` 等过滤；**禁止**仅靠上层内存过滤作为唯一隔离手段。
- 所有外部 I/O 使用带超时的 `context`；错误包装后上抛，不吞异常。

### 5.6 platform —— 平台能力层

**内容**：配置加载、日志、指标、链路追踪、健康检查、通用中间件、（可选）轻量 DI。

**约束**：不承载业务语义；不反向依赖业务层；可提供按 channel 区分的 Auth 中间件工厂，但权限业务规则仍在 app / IAM 域。

### 5.7 pkg —— 公共库

**允许**：`httpx`、`otelx`、`uuidx`、`health`、`cryptox`、`retry`、`ctxutil`。

**禁止**：任何业务概念。判定：**放到另一个项目还能用吗？不能就放回 internal。**

---

## 六、依赖规则

### 6.1 合法依赖

```text
cmd            → 任意层（仅装配本端）
transport      → app、domain、shared（DTO 转换）；禁止 import 他端 transport
app            → domain、shared
infrastructure → domain（实现接口）
platform       → 无业务依赖
domain         → 仅 domain 内部 + 标准库（及极少数无 I/O 的纯工具）
```

### 6.2 禁止依赖

| 禁止项 | 说明 |
|---|---|
| `domain` 导入 `app` / `transport` / `infrastructure` / `platform` | 分层倒置 |
| `transport` 直接导入 `infrastructure` | 绕过应用层 |
| `app` 导入具体仓储实现 | 应依赖接口 |
| `transport/admin` 导入 `transport/biz` 或 `transport/c` | 端隔离破坏 |
| 领域之间互相 import 实现包（如 `order` → `user/app`） | 见 6.3 |
| `pkg` 导入 `internal` | 公共库反向依赖业务 |
| 循环 import | CI 强制检查，阻断合并 |

### 6.3 跨领域调用三种方式

| 方式 | 适用场景 | 说明 |
|---|---|---|
| **接口注入** | 同步、强一致 | 调用方定义接口（如 `UserReader`），`cmd` 注入实现 |
| **领域事件** | 异步、最终一致 | 发布 `OrderCreated`，订阅方独立处理；**默认推荐** |
| **共享契约** | 共用类型 | `internal/shared` 或 `pkg/contract`，**只放类型，不放实现** |

### 6.4 循环依赖预防

- 本地：`go list -f '{{.ImportPath}}: {{.Imports}}' ./...`
- CI：集成循环依赖 / 架构分层检查（如 depguard、自定义 import 规则）。
- 出现循环视为架构违规，必须在 MR 中修复后方可合并。

---

## 七、请求生命周期与上下文

### 7.1 Context 与 Principal

- 每个请求入口创建根 `context`，向下透传，**禁止**在库函数中使用 `context.Background()` 发起外部调用（除进程级后台任务外）。
- 认证成功后注入统一 **Principal**（建议放 `internal/shared/auth`），至少包含：

| 字段 | 说明 |
|---|---|
| `channel` | `admin` / `biz` / `c`，与当前进程一致 |
| `subject_id` | 员工 ID / 商户员工 ID / 消费者 user_id |
| `merchant_id` | B 端必填；A/C 按需 |
| `roles` / `permissions` | 权限点集合（A/B）；C 可简化 |
| `request_id` | 与日志、响应 envelope 一致 |

- 同时透传：`trace_id` / `span_id`、截止时间。
- 禁止将 `context.Context` 存入结构体长期持有；禁止用 context 传递可选业务参数（应用显式参数）。

### 7.2 中间件顺序（建议）

```text
Recover → RequestID/Trace → AccessLog → Metrics → AuthN(本端) → AuthZ → TenantGuard(B) → Timeout → Handler
```

- `AuthN` / `AuthZ` **按 cmd 挂载不同实现**（不同 issuer、密钥、权限源）。
- B 端 `TenantGuard`：校验路径/Body 中的 `merchant_id`（若有）与 Token 内商户一致。

### 7.3 超时分层

| 层级 | 建议 |
|---|---|
| 入口 HTTP/gRPC | 全局/按路由超时（如 5–30s）；C 端读接口可更短 |
| 下游 HTTP/RPC | 小于入口超时，预留缓冲 |
| DB 单次查询 | 明确 statement timeout / context deadline |
| 后台任务 | 独立超时与取消，不绑请求生命周期（除非明确需要） |

---

## 八、代码规范

### 8.1 基础约定

- Go 版本统一，`go.mod` 指定 `toolchain`；CI 与本地一致。
- `go fmt` / `goimports`；`golangci-lint` 不通过则阻断合并。
- 包名小写、简短、无下划线，不与标准库重名；transport 子包可用 `admin` / `biz` / `c`。
- 文件名小写下划线分隔；测试文件 `_test.go`。
- **注释硬约束**见 [§8.6](#86-注释与文档硬约束) 与 [`COMMENT_STANDARDS.md`](./COMMENT_STANDARDS.md)。
- **代码风格硬约束**见 [§8.7](#87-代码编写风格硬约束) 与 [`CODE_STYLE.md`](./CODE_STYLE.md)。
- `make lint` 不通过则阻断合并。

### 8.2 命名

> 完整命名表与正反例见 [`CODE_STYLE.md`](./CODE_STYLE.md) §3（S-02）。

| 元素 | 规范 | 示例 |
|---|---|---|
| 接口 | er/able 结尾或名词 | `OrderRepository`、`UserReader` |
| 实现 | 技术前缀 + 名词 | `mysqlOrderRepository`、`redisOrderCache` |
| 应用服务 | 名词 + Service | `OrderService` |
| Handler | 名词 + Handler（包路径区分端） | `transport/admin.Handler` |
| Command/Query | 动词 + 名词 | `CreateOrderCommand` |
| 事件 | 名词 + 过去式 | `OrderCreated` |
| 错误 | Err + 描述 | `ErrInsufficientStock` |
| 私有实现 | 小写开头 | `orderRepository` |

### 8.3 错误处理

- domain 定义哨兵错误：`var ErrNotFound = errors.New("...")`。
- 使用 `fmt.Errorf("%w", err)` 包装，保留因果链。
- 禁止裸 `errors.New` 散落业务代码；统一收口到 `domain/errors.go` 或 `internal/shared/errors`。
- transport 层将领域错误映射为 HTTP/gRPC 状态码与**稳定业务错误码**；对外 message 按端控制详细程度（C 端更笼统）。
- 禁止忽略 error；`_` 仅在明确无影响时使用并注释原因。
- 关键路径必须带上下文日志字段（`channel`、`order_id`、`merchant_id`、`user_id` 等），但不得记录密钥、完整证件号、银行卡等。

### 8.4 并发与资源

- goroutine 必须有生命周期与退出机制（context 取消、`errgroup`、`WaitGroup`）。
- 禁止在请求处理中裸起 goroutine 且不追踪；后台任务应在 `cmd/worker` 统一托管。
- `defer` 紧跟资源获取。
- 所有外部调用必须带 timeout context。
- channel 注意 nil / 已关闭；避免泄漏。

### 8.5 依赖管理与供应链

> **完整硬约束**见 [`docs/DEPENDENCY_STANDARDS.md`](./DEPENDENCY_STANDARDS.md)（DEP-01～DEP-08）。  
> **许可证策略**见 [`docs/LICENSE_STANDARDS.md`](./LICENSE_STANDARDS.md)（LIC-01～LIC-07）。  
> 门禁：`make vuln` + `make check-deps` + `make check-license`（并入 CI）。

- 使用 Go modules；锁定 `go.sum`；禁止随意 `replace`（例外：ADR + `deps/replace-allowlist.txt`）。
- CI 运行 `govulncheck` 与许可证允许/禁止列表检查；高危漏洞 / 禁止许可证阻断合并。
- 依赖更新：Dependabot（每周）+ Review；新增直接依赖须在 MR 说明用途与许可证。
- 生产镜像构建可开启 SBOM（如 CycloneDX）与镜像签名（按组织要求）。

### 8.6 注释与文档硬约束

> **完整条文**以 [`docs/COMMENT_STANDARDS.md`](./COMMENT_STANDARDS.md) 为准；本节为架构级摘要，二者冲突时以注释规范专文 + lint 配置为准。

**MUST（合并门禁）：**

| ID | 要求 |
|----|------|
| C-01 | 非 `main` 包必须有包注释（或 `doc.go`） |
| C-02 | 所有导出符号必须有 godoc，且**以声明名称开头** |
| C-04 | `pkg/` 导出 API 注释须达到可独立复用（行为/错误/并发/零值） |
| C-05 | 禁止复述标识符的无意义注释 |
| C-06 | `nolint` 必须带 linter 名与原因 |
| C-07 | 忽略 error 必须注释理由 |
| C-10 | 注释与代码同 MR 保持一致 |

**分层重点：** `domain` 写不变量与状态迁移；`app` 写事务/幂等；`transport` 写端别与错误码映射；`pkg` 写契约与并发安全。

**工具强制：** `.golangci.yml` 启用 `revive`（`exported`、`package-comments`）与 `nolintlint`；本地与 CI 统一执行 `make lint`。

**非目标：** 不强制英文句号（不启用 `godot`），以支持中文 `。` 结尾的 godoc。

### 8.7 代码编写风格硬约束

> **完整条文**以 [`docs/CODE_STYLE.md`](./CODE_STYLE.md) 为准；本节为架构级摘要。

**MUST（合并门禁）：**

| ID | 要求 |
|----|------|
| S-01 | `gofmt` + `goimports`（local-prefix 本模块） |
| S-02 | 命名符合风格专文 §3（禁 I 前缀、禁随意缩写） |
| S-03 | `context.Context` 第一参数（如需要）；`error` 最后返回值 |
| S-04 | context key 使用未导出类型 |
| S-05 | 错误早返回，禁深层 `if err == nil` |
| S-06 | 禁点导入；空白导入须注释 |
| S-07 | 接口定义在消费侧 |
| S-08 | 正常路径禁 panic（`Must*` 除外且须文档化） |
| S-09 | 禁导出函数返回未导出类型 |
| S-10 | 忽略 error 必须注释原因 |
| S-11 | 控制函数体/复杂度；超限须拆分或 MR 说明 |
| S-12 | goroutine 可取消；共享状态有同步 |

**工具强制：** `.golangci.yml` 启用 goimports、revive 风格规则、errcheck、staticcheck、gocritic、misspell、whitespace、nakedret、usestdlibvars 等；本地与 CI 统一 `make fmt && make lint`。

---

## 九、一致性、事务与幂等

### 9.1 事务边界

- **事务只在 app 层开启与提交**；domain 不感知事务实现。
- 单用例单事务为默认；长事务、跨远程调用事务**禁止**（改用 Outbox / 补偿 / 最终一致）。
- 仓储方法保持细粒度；跨聚合一致性优先用领域事件 + 幂等消费者。

### 9.2 幂等

- 写接口（创建订单、支付回调、重试投递）必须可幂等；**C 端与支付相关接口强制** `Idempotency-Key` 或业务唯一键。
- 推荐：客户端 `Idempotency-Key` 或业务唯一键（如 `out_trade_no`）+ 去重表 / 唯一索引。
- 消费者处理消息必须幂等；至少一次投递下允许重复，结果须一致。

### 9.3 并发控制

- 聚合更新使用乐观锁（`version` 字段）或条件更新；冲突返回可重试错误。
- 库存、余额等热点资源需有明确策略（乐观锁 / 预扣 / 分段），禁止无保护的读改写。

### 9.4 分布式场景

| 场景 | 策略 |
|---|---|
| 本地多表 | 单库事务 |
| 跨服务写 | Outbox / 事件驱动 + 补偿，避免 2PC |
| 支付回调 | 验签 + 幂等 + 状态机严格转移 |

---

## 十、安全规范

> **硬约束全文**见 [`docs/SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)（SEC-01～SEC-14）。门禁：`make secrets`（gitleaks）、`make lint`（gosec）、`make check-debug`、MR 模板。

### 10.1 三端认证与鉴权（定稿）

| 端 | AuthN | AuthZ | 额外要求 |
|---|---|---|---|
| **A** | 员工账号（密码/SSO）+ 独立 Token 域 | RBAC：菜单 / 按钮 / API 权限点 | 强审计；可选 MFA；Swagger/pprof 生产关闭或强隔离 |
| **B** | 商户主体 + 商户员工账号 + 独立 Token 域 | 商户内 RBAC | 所有读写带 `merchant_id`；app 强制 + 仓储条件兜底 |
| **C** | 消费者账号（手机号 / OAuth 等）+ 独立 Token 域 | 资源归属（本人） | 对外字段最小化；登录防爆破；接口级限流 |

通用要求：

- 认证在 transport 中间件完成；注入不可伪造的 Principal（见 7.1）。
- **鉴权默认拒绝**：未配置权限的接口不可匿名访问（健康检查等白名单除外）。
- Token 校验失败与权限不足返回区分错误码；日志记录主体与资源，不记录完整 Token。
- **三端 issuer / audience / 签名密钥分离**；校验时校验 `channel` 与当前进程一致。

### 10.2 输入与输出

- 所有外部输入校验：类型、长度、范围、枚举；拒绝未知字段（按**该端** API 契约）。
- 防注入：SQL 一律参数化；禁止字符串拼接 SQL。
- 输出编码：HTML/日志场景注意转义；错误信息不泄露内部堆栈给终端用户（生产环境，尤其 C 端）。

### 10.3 密钥与敏感数据

- 密钥、密码、私钥不得入库代码或配置样例；使用环境变量 / KMS / Vault 等注入。
- 日志、Trace、错误上报中对 PII / 密钥脱敏（手机号、证件、Token 等）。
- 静态密钥轮换有流程；泄露按安全事件升级处理。
- A 端导出含 PII 的数据须审批或审计字段齐全。

### 10.4 传输与会话

- 生产强制 TLS；内部服务间按组织策略启用 mTLS 或服务网格。
- Cookie（若使用）设置 `HttpOnly`、`Secure`、合理 `SameSite`。
- A/B 登录防爆破；C 端登录与验证码接口额外限流。
- A 端网络宜内网或 VPN；公网暴露须 WAF / IP 允许列表等补偿控制。

### 10.5 依赖与运行面

- 容器非 root；只读根文件系统（可行时）；最小 capabilities。
- 定期漏洞扫描（依赖 + 镜像）；禁止使用已知高危且无缓解的组件发版。
- 调试端点（pprof、Swagger UI）生产默认关闭或强鉴权 + 网络隔离（A 端尤甚）。

### 10.6 安全 Checklist（MR）

- [ ] 新接口挂载在正确端的 cmd / transport，且鉴权与权限点正确
- [ ] B 端具备 `merchant_id` 隔离；C 端具备资源归属校验
- [ ] 无跨端 Token / DTO / Handler 复用
- [ ] 无 SQL 拼接 / 命令注入风险
- [ ] 密钥未进入仓库或日志；C 端响应无运营敏感字段
- [ ] 写操作具备幂等；A/B 关键操作具备审计

---

## 十一、可靠性与弹性

> **完整硬约束**见 [`docs/RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)（REL-01～REL-08）。  
> 门禁：`make check-reliability`（并入 CI job `arch`）+ Review + MR 模板。

### 11.1 超时、重试、熔断

| 策略 | 要求 |
|---|---|
| 超时 | 所有外部调用必有；禁止无限等待 |
| 重试 | 仅对幂等/可安全重试错误；指数退避 + 抖动；有上限 |
| 熔断 / 舱壁 | 下游持续失败时快速失败，避免雪崩；关键路径隔离连接池 |
| 限流 | **按端配置**：C 端最严，B 按商户，A 适中；超限返回明确错误码 |

### 11.2 优雅降级

- 非关键依赖（个性化推荐、次要缓存）失败时可降级，不得拖垮主链路。
- 降级行为必须可观测（指标 + 日志），并在文档中说明业务影响。

### 11.3 容量与背压

> **完整硬约束**见 [`docs/PERFORMANCE_STANDARDS.md`](./PERFORMANCE_STANDARDS.md)（PERF-01～PERF-07）。  
> 门禁：`make check-perf`（并入 CI job `arch`）+ 关键路径 Benchmark 登记。

- 三端 API 与 worker 的 HTTP Server、DB 连接池、并发数**分别可配置**。
- 队列消费者控制并发与 ack 时机；积压可告警。
- 列表接口强制分页与 `page_size` 上限。
- 已登记热点须有可运行的 `Benchmark*`（见 `docs/perf/BASELINES.md`）。

### 11.4 SLO 与告警（建议基线）

| 信号 | 建议 |
|---|---|
| 可用性 | **按可执行文件**分别定义 SLO（如月度 99.9%） |
| 延迟 | 各端 P99 接口延迟告警 |
| 错误率 | 5xx / 业务致命错误率告警 |
| 依赖 | DB 连接池耗尽、MQ 消费滞后、缓存错误率 |
| 饱和 | CPU / 内存 / Goroutine 异常增长 |

具体阈值写入 `docs/runbooks/`，与值班手册对齐。

---

## 十二、可观测性

> **完整硬约束**见 [`docs/OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)（OBS-01～OBS-08）。  
> **审计硬约束**见 [`docs/AUDIT_STANDARDS.md`](./AUDIT_STANDARDS.md)（AUD-01～AUD-09）；门禁：`make check-audit`（并入 CI job `arch`）。  
> 门禁：`make check-observability`（并入 CI job `arch`）+ Review + MR 模板。

| 维度 | 要求 |
|---|---|
| **日志** | 结构化 JSON；含 `trace_id`、`span_id`、`service`（如 `admin-api`）、`channel`、`env`、`request_id`；按环境定级别 |
| **指标** | RED + 业务关键指标；按 `service`/`channel` 分标签；队列积压、缓存命中率、连接池使用率 |
| **追踪** | OpenTelemetry 全链路；跨服务 W3C Trace Context / 约定 header 透传 |
| **健康检查** | `/healthz` 存活；`/readyz` 就绪（探测 DB/MQ 等关键依赖） |
| **审计** | 见 AUD 专文：事件目录、字段字典、追加写存储、保留期；经 `internal/shared/audit` 发出 |

**日志规范补充**：

- 业务失败用 `Warn`/`Error` 分级；预期内的客户端错误（4xx）默认不打 Error，避免告警噪音。
- 同一请求内关键字段键名全局统一（`user_id`、`merchant_id`、`channel`）。

---

## 十三、测试规范

> **完整硬约束**见 [`docs/TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)（TEST-01～TEST-08）。  
> 门禁：`make test-ci` / CI job `test`（含 `scripts/ci/check-test-gates.sh`）。

### 13.1 分层放置

| 类型 | 位置 | 说明 |
|---|---|---|
| 单元测试 | 同包 `_test.go` | domain **必须**；核心 domain 包覆盖率 ≥ 70%（硬门槛） |
| 集成测试 | `tests/integration/` | testcontainers 起真实 DB/MQ；含商户隔离用例 |
| 端到端 | `tests/e2e/{admin,biz,c}/` | 分别拉起对应 api 进程 |
| Benchmark | `BenchmarkXxx` | 核心热路径按需 |

### 13.2 原则

- domain 纯逻辑**必须单测**。
- infrastructure **不得连接生产库**；用容器或 mock。
- 测试数据用 factory/builder；禁止大段复制 fixture。
- 多分支用表驱动测试；**B 端隔离、C 端越权**必须有自动化用例（基线函数名见 TEST-03）。
- mock 由 `mockery` 生成，放在 `internal/<domain>/mocks/`；**禁止手改**生成物（见 [`CODEGEN_STANDARDS.md`](./CODEGEN_STANDARDS.md)）。
- CI：`make test-ci` 失败阻断（含 `-race`、domain 覆盖率、必测场景）。

### 13.3 测试环境隔离

- 使用独立 schema / 随机库名 / 事务回滚等策略，保证并行安全。
- 密钥与外部 SaaS 使用 mock 或 sandbox，禁止用生产凭据跑 CI。

---

## 十四、数据与迁移

> **完整硬约束**见 [`docs/DATA_STANDARDS.md`](./DATA_STANDARDS.md)（DATA-01～DATA-08）。  
> 门禁：`make check-migrations`（并入 CI job `arch`）+ Review + MR 模板。

### 14.1 模型与迁移

- **领域模型 ≠ 数据库模型**；转换在 infrastructure。
- 迁移脚本：`migrations/<NNNNNN>_<desc>.up.sql` / `.down.sql`，**必须可回滚**（不可逆变更需 ADR + 运维方案）。
- 一次 MR 优先只改一个域的表结构；SQL 必审。
- **禁止**在业务进程启动时自动 migrate（仅 `cmd/migrate` 或发布流水线执行）。
- 主键策略统一（雪花 / UUID）；`created_at`、`updated_at`、软删除 `deleted_at` 约定统一；时区存储用 UTC。
- 禁止 `SELECT *`；列表必分页且有上限；大结果集走游标/导出任务（导出优先走 A/B，并审计）。
- 索引变更评估锁表与性能影响；大表变更走在线 DDL / 分步发布。
- 备份与恢复：生产库有定期备份与恢复演练要求（见 runbook / DATA-07）。

### 14.2 多租户与行级隔离（定稿）

- **起步策略**：共享库表 + **`merchant_id` 行级隔离**（B 端数据）。
- 商户相关表必须具备 `merchant_id`（或等价）索引；列表 / 导出 / 更新 / 删除的 SQL **必须**带租户条件。
- **禁止**「先查全量再在内存按商户过滤」作为唯一防护。
- C 端用户资源以 `user_id`（或 subject）约束；与商户相关的 C 端读路径仍须防止横向越权。
- A 端可跨商户查询，但须权限点控制，并在审计中记录查询范围。
- **演进**：单商户数据量或合规要求升高时，可评估分库 / 分 schema，另立 ADR。

---

## 十五、缓存与异步消息

### 15.1 缓存

- 缓存是性能优化，不是数据源真相；真相在持久化存储。
- Key 命名规范：`{env}:{channel}:{merchant_id?}:{domain}:{entity}:{id}`；必须设置 TTL。
- Redis 客户端 / 分布式锁 / KV 缓存原语见 `pkg/redis`（`Key` / `Cache` / `Lock`）；进程装配留 platform。
- 更新策略明确：Cache-Aside / Write-Through 等，并处理穿透、击穿、雪崩（空值缓存、互斥、随机 TTL）。
- 禁止缓存未脱敏的敏感信息；权限与商户数据缓存失效时不得导致越权（缓存条目必须绑定租户/用户维度）。

### 15.2 消息与事件

- 领域事件语义清晰、版本化（schema）；消费者按版本兼容处理。
- 投递语义默认按**至少一次**设计；消费者幂等。
- 失败：可重试错误进重试队列；毒消息进死信（DLQ）并告警。
- 发布与本地事务：优先 Transactional Outbox，避免「库已写、事件未发」。
- 事件载荷含必要的 `merchant_id` / `user_id`，供消费者做隔离与幂等键。

---

## 十六、API 规范

> **完整硬约束**见 [`docs/API_STANDARDS.md`](./API_STANDARDS.md)（API-01～API-12）。  
> 门禁：`make openapi` / CI job `openapi`（oasdiff validate + breaking）。

### 16.1 契约优先（分端）

- 协议定义分端维护：
  - `api/openapi/admin.yaml`
  - `api/openapi/biz.yaml`
  - `api/openapi/c.yaml`
  - 共享信封/分页/错误响应：`api/openapi/components.yaml`
- 实现与契约在 CI 中校验（oasdiff validate；相对基线 breaking，无 major 提升则阻断）。
- 错误码枚举共用（`internal/shared/apperr`），**响应字段按端裁剪**，以各端 OpenAPI 为准。

### 16.2 REST 约定

- URL 前缀定稿：
  - A：`/admin/v1/...`
  - B：`/biz/v1/...`
  - C：`/c/v1/...`
- 资源名词复数；URL 小写连字符；动作用 HTTP Method。
- 统一响应 envelope：

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "trace_id": "abc123"
}
```

- 分页：`page`、`page_size`、`total`；`page_size` 上限 100（导出另议，且仅 A/B）。
- 时间：RFC3339 或 Unix 秒，全项目一致；对外文档写明时区语义（建议 UTC）。
- 变更：只增不减；废弃字段标 `deprecated`，至少保留一个大版本；破坏性变更走版本前缀（`/v2`）或独立协商。

### 16.3 分端强调点

| 端 | 强调 |
|---|---|
| A | 审计、导出管控、权限点与操作日志；破坏性操作二次确认（产品层） |
| B | 路径或 Body 中的 `merchant_id` 必须与 Token 一致；商户内权限点 |
| C | 幂等、限流、字段最小化；禁止泄露内部/运营字段 |

### 16.4 gRPC / Protobuf

- 不改已有 field number；不修改已有字段语义。
- 枚举只追加；删除用 reserved。
- 错误使用丰富错误模型（status + details），与业务错误码映射表维护在文档中。
- 若对内 gRPC 跨进程，仍须携带 channel / merchant / subject 元数据并校验。

### 16.5 兼容与弃用

- 公开 API 变更需更新对应端的 OpenAPI 与 CHANGELOG / 发布说明。
- 弃用期通知调用方；到期删除须有流量确认（指标为零或白名单豁免）。

---

## 十七、配置、密钥与多环境

> **完整硬约束**见 [`docs/CONFIG_STANDARDS.md`](./CONFIG_STANDARDS.md)（CFG-01～CFG-08）。  
> 门禁：`make check-config`（并入 CI `secrets`）+ `Validate` 单测 + gitleaks。

### 17.1 配置加载

- 来源：`configs/<cmd>.yaml.example` + 环境变量（`ADMIN_FRAME_*`）+（可选）配置中心。
- 优先级：**环境变量 > 配置文件 > 默认值**（配置中心若引入须 ADR 并更新专文）。
- 各 cmd **独立监听地址/端口**与限流等段；DB/Redis 等共享连接参数可通过公共配置段或环境变量复用，由 `platform` 统一加载。
- 结构体提供 `Validate()`；启动时校验必填项、范围与互斥项（含 `env=prod` ⇒ `demo_mode=false`），失败则拒绝启动。
- 禁止业务代码硬编码地址、超时、功能开关。

### 17.2 多环境

| 环境 | 要求 |
|---|---|
| local | 可分别启动三端 api；可用 docker-compose |
| dev / test | 与生产拓扑近似；数据可销毁 |
| staging | 生产同配（缩容）；发布前验证 |
| prod | 变更受控；密钥隔离；审计开启；三端可独立扩缩；禁演示鉴权 |

- 仓库内仅提交 `*.yaml.example`，不含真实密钥；本地 `configs/*.yaml` 由 `.gitignore` 忽略。

### 17.3 特性开关

- 高风险逻辑用 feature flag；默认关闭；可按 `channel` / `merchant_id` / 百分比灰度。
- Flag 配置可观测；长期 flag 定期清理，避免技术债。

---

## 十八、构建、发布与运维

> **完整硬约束**见 [`docs/RELEASE_STANDARDS.md`](./RELEASE_STANDARDS.md)（RELS-01～RELS-08）与 [`docs/RUNBOOK_STANDARDS.md`](./RUNBOOK_STANDARDS.md)（RB-01～RB-07）。  
> 门禁：`make check-release` / `make check-runbooks`（并入 CI job `arch`）+ 发布 Checklist + Review。

### 18.1 Makefile 目标（基线）

```make
make build              # 编译全部 cmd → bin/admin-api bin/biz-api bin/c-api bin/worker ...
make build-admin        # 仅 admin-api
make build-biz
make build-c
make run-admin          # 本地运行 A 端
make run-biz
make run-c
make test               # 单元 + 集成测试
make lint
make fmt
make mock
make migrate-up
make vuln
make docker             # 构建全部业务镜像
make docker-admin       # 单端镜像（多 stage target）
```

### 18.2 Dockerfile

- 多阶段构建：builder 编译，runtime 仅二进制 + 必要配置。
- **同一 Dockerfile 多 target**（或 build-arg `CMD`）产出 `admin-api` / `biz-api` / `c-api` / `worker` 镜像。
- 不含源码与 `.git`；非 root 用户；固定基础镜像 tag（含 digest 更佳）。
- 禁止 `latest`；`HEALTHCHECK` 指向 `/healthz` 或等价探针。
- 构建参数注入版本：`version` / `commit` / `build_time`，可通过 `/version` 或 metrics 暴露。

### 18.3 CI 流水线（门禁）

> **完整硬约束**见 [`docs/CI_STANDARDS.md`](./CI_STANDARDS.md)。  
> 实现：GitHub [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)；Gitee [`.workflow/`](../.workflow/)。本地对齐：`make ci`。

**当前必跑：** 格式 → `go mod tidy` → lint（gosec）→ 测试（race + TEST 门禁）→ **govulncheck + 依赖/许可证门禁** → transport 隔离 → **gitleaks + 调试端点 + 配置样例门禁** → OpenAPI → 全量构建。

**规划中（专文落地后纳入）：**

1. 更完整的许可证专文（LIC）与组织策略对齐  
2. 集成测试容器（testcontainers）常态化  
3. 发版流水线内镜像扫描与自动推送（工具与仓库按组织接入；规范见 RELS）

### 18.4 发布策略

| 策略 | 适用 |
|---|---|
| 滚动发布 | 默认；**三端可独立发布** |
| 蓝绿 / 金丝雀 | 高风险变更（可只对单端） |
| 数据库变更 | expand/contract：先兼容发布，再清理旧列 |

- 发布必须可回滚；迁移与代码兼容顺序写入 MR 说明。
- 生产变更关联工单/变更窗口（按组织流程）。
- `deploy/` 中三端独立 Deployment / Service / HPA，互不影响扩缩。

### 18.5 运行与值班

- 就绪后才接入流量（K8s readiness）。
- 关键事故按 runbook 执行（`docs/runbooks/` + `ALERTS.md`）；事后复盘（Postmortem）沉淀到 `docs/`。
- 禁止在生产直接 `kubectl exec` 改数据作为常规手段；应急操作需双人与审计。

---

## 十九、治理与演进

> **完整硬约束**见 [`docs/GIT_STANDARDS.md`](./GIT_STANDARDS.md)（GIT-01～GIT-08）。  
> 门禁：平台分支保护（人工）+ `make check-git`（并入 CI job `arch`）+ PR 模板。

### 19.1 演进路线

| 阶段 | 特征 | 动作 |
|---|---|---|
| **起步期** | 团队小、边界不清 | 三 cmd + 共享 domain；transport 按端分开；守住接口隔离 |
| **成长期** | 规则复杂、多人协作 | 补全四层；领域事件与 Outbox；完善 B 租户与 A 审计 |
| **稳定期** | 某端或某域独立迭代 | 按域或按端拆服务；复用 `pkg` 与 `platform` |

**拆分信号**（满足 2 条以上可评估独立服务）：

- 独立发布节奏与扩缩容需求（常见：C 端流量远高于 A）  
- 数据需独立存储与治理  
- 跨域调用已成为性能或稳定性瓶颈  
- 有独立团队负责该域或该端  

### 19.2 ADR（架构决策记录）

重大决策（选型变更、拆服务、一致性模型、安全模型调整、租户模型变更）必须写 ADR，存放于 `docs/adr/`：

```text
docs/adr/
├── 0001-record-architecture-decisions.md
├── 0002-use-modular-monolith-multi-cmd.md
├── 0003-abc-channel-isolation.md
└── ...
```

模板见 [附录 C](#附录-cadr-模板)。

### 19.3 所有权

- 每个业务域指定 Code Owner（`CODEOWNERS`）。
- 各端 transport 与对应 OpenAPI 可由端负责人共同 Review。
- `platform`、CI、安全基线由平台/架构组维护。
- 本文档的修改需架构 Owner Review。

---

## 二十、架构合规 Checklist

**MR 自检（作者）+ Reviewer 抽检**

**结构与依赖**

- [ ] 代码落在正确的层与域
- [ ] Handler 落在正确端的 `transport/<channel>`，且仅由对应 cmd 注册
- [ ] 未串用他端 DTO / Handler / Token
- [ ] `domain` 未引入外部框架/I/O 依赖
- [ ] 无跨领域直接 import 实现；无循环依赖
- [ ] Handler 无业务规则；Service 依赖接口而非实现

**三端与数据范围**

- [ ] B 端：`merchant_id` 在 app + 仓储均有约束
- [ ] C 端：资源归属校验；响应字段已按契约裁剪
- [ ] A 端：权限点 + 关键写/导出审计

**正确性与弹性**

- [ ] 外部调用均带超时 context
- [ ] 写路径具备幂等或唯一约束（C/支付尤甚）
- [ ] 事务边界清晰；无跨远程分布式大事务
- [ ] error 被处理或正确包装；关键路径有 `channel` 等日志字段

**安全**

- [ ] 本端 AuthN/AuthZ 正确；无跨端身份冒用可能
- [ ] 无密钥、无未脱敏 PII 进入仓库或日志
- [ ] SQL 参数化；输入已校验

**数据与契约**

- [ ] 迁移含 down（或不可逆方案已评审）
- [ ] 对应端 OpenAPI 已更新；破坏性变更已 major 提升（见 `API_STANDARDS.md`）
- [ ] 审计日志覆盖关键操作（如适用）

**质量门禁**

- [ ] 新增/变更单测充分；隔离/越权场景有覆盖（见 `TESTING_STANDARDS.md`）
- [ ] 新增包有包注释；新增/变更导出符号有合格 godoc（见 `COMMENT_STANDARDS.md`）
- [ ] 命名/签名/错误处理符合 `CODE_STYLE.md`（S-01～S-12）
- [ ] 无空 TODO；`nolint` / 忽略 error 均有说明
- [ ] `make ci`（或至少 `make fmt && make lint && make test && make vuln`）通过
- [ ] CI workflow 绿灯（若已配置分支保护）
- [ ] 需要时已补充/更新 ADR 与 runbook

---

## 附录

### 附录 A：快速上手

```bash
git clone <repo>
cd myproject

cp configs/admin.yaml.example configs/admin.yaml
cp configs/biz.yaml.example   configs/biz.yaml
cp configs/c.yaml.example     configs/c.yaml

# 本地依赖
docker compose up -d

make build
./bin/admin-api   # A 端，默认见 configs/admin.yaml
./bin/biz-api     # B 端
./bin/c-api       # C 端
```

细节见 `docs/` 或 `scripts/`。

### 附录 B：参考

- [工程化注释规范（硬约束）](./COMMENT_STANDARDS.md)
- [工程化代码编写风格规范（硬约束）](./CODE_STYLE.md)
- [工程化 CI 流水线规范（硬约束）](./CI_STANDARDS.md)
- [工程化安全规范（硬约束）](./SECURITY_STANDARDS.md)
- [工程化 API 契约规范（硬约束）](./API_STANDARDS.md)
- [工程化测试规范（硬约束）](./TESTING_STANDARDS.md)
- [工程化依赖与供应链规范（硬约束）](./DEPENDENCY_STANDARDS.md)
- [工程化配置与多环境规范（硬约束）](./CONFIG_STANDARDS.md)
- [工程化数据与迁移规范（硬约束）](./DATA_STANDARDS.md)
- [工程化可靠性与弹性规范（硬约束）](./RELIABILITY_STANDARDS.md)
- [工程化可观测性规范（硬约束）](./OBSERVABILITY_STANDARDS.md)
- [工程化 Git 协作规范（硬约束）](./GIT_STANDARDS.md)
- [工程化发布与镜像规范（硬约束）](./RELEASE_STANDARDS.md)
- [工程化 Runbook 与值班规范（硬约束）](./RUNBOOK_STANDARDS.md)
- [工程化审计规范（硬约束）](./AUDIT_STANDARDS.md)
- [工程化许可证与开源合规规范（硬约束）](./LICENSE_STANDARDS.md)
- [工程化代码生成规范（硬约束）](./CODEGEN_STANDARDS.md)
- [工程化性能与容量规范（硬约束）](./PERFORMANCE_STANDARDS.md)
- [工程硬约束专文路线图](./standards-roadmap.md)
- [Standard Go Project Layout](https://github.com/golang-standards/project-layout)
- Clean Architecture / Hexagonal Architecture
- Domain-Driven Design（Eric Evans）
- Effective Go、[Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- OpenTelemetry 规范
- 12-Factor App（配置、日志、进程等原则的参考，按需裁剪）

### 附录 C：ADR 模板

```markdown
# ADR-XXXX：标题

- 状态：提议 | 已接受 | 已废弃
- 日期：YYYY-MM-DD
- 决策人：

## 背景

## 决策

## 备选方案

## 后果（正面 / 负面 / 风险）

## 后续行动
```

### 附录 D：术语表

| 术语 | 含义 |
|---|---|
| Channel | 业务端别：`admin`（A）/ `biz`（B）/ `c`（C） |
| Merchant | 商家/企业租户；B 端隔离依据，字段常为 `merchant_id` |
| Principal | 认证后的主体上下文（channel、subject、商户、角色等） |
| Transport | 接入层包，按端拆分 Handler/DTO/路由 |
| 聚合根 | 一致性边界内的入口实体 |
| DTO | 跨进程/跨层传输对象，非领域模型；本项目按端维护 |
| Outbox | 本地事务内写入事件表，异步可靠发布 |
| SLO | 服务等级目标 |
| ADR | 架构决策记录 |
| 幂等 | 同一请求执行多次与一次效果相同 |
