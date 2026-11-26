# 通知渠道：短信 / 邮件 / 推送 模块化驱动规划

> **状态**：核心契约、L1 驱动（短信阿里云、邮件 SMTP、推送 APNs/FCM）与**配置驱动装配**均已落地；`cmd/worker` 按 `notification.channels` 构造渠道路由，prod/staging 禁止 memory  
> **关联**：[`pkg-roadmap.md`](./pkg-roadmap.md) §2.6、[`ARCHITECTURE.md`](./ARCHITECTURE.md) §5.7 / §6 / §11 / §12、[`RELIABILITY_STANDARDS.md`](./RELIABILITY_STANDARDS.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`OBSERVABILITY_STANDARDS.md`](./OBSERVABILITY_STANDARDS.md)、[`CONFIG_STANDARDS.md`](./CONFIG_STANDARDS.md)、[`DEPENDENCY_STANDARDS.md`](./DEPENDENCY_STANDARDS.md)、[`TESTING_STANDARDS.md`](./TESTING_STANDARDS.md)  
> **最后更新**：2026-11-05（补状态与变更方式字段；关联章节按 `pkg-roadmap` 重编号同步为 §2.6）  
> **变更方式**：渠道契约、错误语义、驱动矩阵或安全红线变更须同步 `pkg/{sms,email,push}` 契约、契约测试与本文配置 / 观测章节，经安全 Review 后合入
> **目标读者**：后端 / 平台装配 / 安全 Review / SRE

---

## 0. 结论摘要（TL;DR）

1. **三个包、三个职责**：`pkg/sms`、`pkg/email`、`pkg/push` 各自提供「渠道契约 + 可选驱动」；不新建 `pkg/notify` 聚合包，避免把模板、路由、偏好等业务能力塞进 `pkg`。
2. **业务编排留在 internal**：模板中心、渠道路由/主备、频控策略、退订/偏好、发送去重、Outbox/Pub-Sub 统一放 `internal/notify`（M4 再开，且仅在首个调用方出现后）。
3. **驱动只做一次尝试**：超时、重试、熔断、限流**不在驱动内**，由编排层用 `pkg/httpclient` / `pkg/retry` / `pkg/circuitbreaker` / `pkg/ratelimit` 组合，避免重试放大。
4. **默认安全**：模板优先、禁止裸内容；生产禁止 memory/fake 驱动与明文 SMTP；密钥只来自环境变量/KMS；手机号、邮箱、设备 Token、验证码不得进入日志、Trace、错误消息与指标标签。
5. **默认零新依赖**：L1 用标准库 + `pkg/httpclient` 直接对接厂商 HTTP API 与 SMTP；官方 SDK 作为 L2 可选，仅出现在对应驱动子包（对齐 `pkg/storage` 的 L1/L2 模式与 DEP-06）。
6. **可测试是一等公民**：内置 `memory` 驱动与 `smstest` / `emailtest` / `pushtest` 契约测试；所有驱动用 `httptest` / 本地 SMTP fake 覆盖，CI 不访问真实厂商。
7. **优先级仍为 P4**：先做契约与 fake（M0），再按真实场景依次落地 email → sms → push；不要为了「路线图打勾」提前引入厂商 SDK 与生产配置。

| 渠道 | 首个合理场景 | 建议驱动顺序 | 主要风险 |
|------|--------------|--------------|----------|
| `pkg/email` | A 端告警、运营通知、导出完成通知 | SMTP（自建/企业邮箱）→ SES 等 HTTP API | 可达性（SPF/DKIM/DMARC）、附件与 Header 注入 |
| `pkg/sms` | C 端登录/支付验证码、订单状态 | 阿里云 → 腾讯云 → 华为云 | 合规报备、短信轰炸、模板与签名审核 |
| `pkg/push` | C 端 App 订单/物流通知 | APNs + FCM → 国内厂商通道 | Token 失效、Payload 超限、厂商错误码差异 |

---

## 1. 定位与边界

### 1.1 分层

```text
internal/<domain>/app
        │ 只表达「发什么」，不关心厂商
        ▼
internal/notify（M4）              ← 业务编排：模板、路由、频控、去重、偏好、Outbox
        │ 只依赖三个渠道契约
        ▼
pkg/sms        pkg/email        pkg/push
        │ blank-import 可选驱动
        ├── sms/aliyun   sms/tencent   sms/huawei
        ├── email/smtp   email/ses
        └── push/apns    push/fcm      push/hms …
```

### 1.2 放置边界

| 放哪里 | 内容 | 判定 |
|--------|------|------|
| `pkg/sms` / `pkg/email` / `pkg/push` | 渠道契约、消息校验、错误语义、驱动注册表、memory/fake、契约测试 | 换一个项目可直接拷走 |
| `pkg/{sms,email,push}/<driver>` | 厂商协议、签名、错误码映射、JWT/Token 缓存 | 只有引入该驱动才链接对应代码 |
| `internal/notify` | 模板注册与渲染、渠道选择/主备、频控、退订、去重、发送记录、回执回调 | 含业务规则与持久化 |
| `internal/platform` | 读 YAML/env、选 driver、blank-import、注入 Observer/Clock、注册 `/readyz` | 只做装配 |
| `internal/<domain>` | 触发时机与文案语义（如「订单已发货」） | 领域知识 |

### 1.3 关键决策（ADR 摘要）

| ID | 决策 | 理由 | 重新评估触发条件 |
|----|------|------|------------------|
| D1 | 不设 `pkg/notify` 聚合包 | 三渠道契约差异大；聚合包会演化成业务杂物间，违背「一个包一个职责」 | 两个以上项目出现完全相同的跨渠道契约，且三包需要同步发版 |
| D2 | 驱动内不重试、不熔断、不限流 | 避免「重试 × 重试」放大；策略集中在编排层，便于按供应商区分 | 厂商明确要求客户端内部重试（当前无） |
| D3 | 驱动不写日志、不建 Span | 统一由编排层/`Observer` 输出，天然避免 PII 泄漏与重复打点 | 需要驱动级调试且能证明不含 PII |
| D4 | L1 走 HTTP/SMTP，不默认引入厂商 SDK | 控制供应链与二进制体积；签名算法可用标准库实现并 golden 测试 | 需要厂商独有能力（批量、回执、富媒体）且 HTTP 不可达 |
| D5 | 模板优先，裸内容默认禁止 | 防内容注入、便于合规审核与多语言；裸内容仅本地/测试显式开启 | 运营必须自由文本且完成安全评审 |
| D6 | 观测通过 `Observer` 注入，不直接依赖 `pkg/metrics` | 避免重复注册指标与全局状态；便于单测断言事件 | 三个包都需要同一套指标且注入成本过高 |

---

## 2. 非目标

- 模板管理后台、可视化编辑器、营销自动化、A/B 实验。
- 收件箱解析、退信/投诉自动处理（M5 之后按需）。
- 厂商账单、计费与对账。
- 跨渠道统一编排（站内信 + 短信 + Push 降级链）——属于 `internal/notify`。
- 在 `pkg` 内做用户偏好、退订、黑名单等业务规则。

---

## 3. 统一契约设计

三个包的根包保持**同构**（相同命名、相同错误语义、相同注册表），但各自独立、可单独拷贝。公共横切能力复用现有 `pkg/*`，不复制基础设施。

### 3.1 共通约定

| 约定 | 说明 |
|------|------|
| `ctx` | 所有 `Send` 必须接受 `context.Context`；`nil` 按 `context.Background()` 处理（与 `pkg/storage.Open` 一致），但仍要求调用方传 deadline（REL-01） |
| 零值 | `Message` 零值非法，`Validate` 返回 `ErrInvalidMessage`；`Receipt` 零值表示「未受理」 |
| 并发 | `Sender` 必须并发安全；`Close` 幂等；`Close` 后 `Send` 返回 `ErrClosed` |
| 幂等 | `IdempotencyKey` 为调用方提供的稳定键（≤ 128 字节），仅用于编排层去重与排障关联；**不承诺**厂商侧 exactly-once |
| PII | `Message.String()` 不得输出收件人；驱动错误消息不得包含手机号、邮箱、Token、正文、验证码 |
| 观测 | 只通过 `Observer` 与返回值暴露；驱动不写日志、不注册指标 |
| 时间 | 回执时间、Token 过期判断使用注入的 `clock.Clock`，保证测试确定性 |

### 3.2 错误模型

每个包提供 `ProviderError`，包装底层语义错误，并保留厂商原始码与建议等待时间：

```go
// ProviderError 是渠道错误的结构化视图。
//
// Err 必须是 pkg/errors 的语义错误（InvalidArgument / PermissionDenied /
// ResourceExhausted / Unavailable / DeadlineExceeded / Internal），
// 从而可被 pkg/retry 的默认 RetryIf 正确识别。
type ProviderError struct {
	Provider   string
	Code       string        // 厂商原始码，仅排障；禁止进入指标标签
	RetryAfter time.Duration // 厂商建议等待时间；0 表示未提供
	Err        error
}

// Error 返回脱敏后的错误文本，不包含收件人与请求体。
func (e *ProviderError) Error() string

// Unwrap 支持 errors.Is / errors.As 穿透到语义错误。
func (e *ProviderError) Unwrap() error { return e.Err }

// Retryable 代理 pkg/errors.RetryableOf(e.Err)。
func (e *ProviderError) Retryable() bool
```

**哨兵错误**（`errors.NewStd` 创建，支持 `errors.Is`；下表为三包并集，按渠道裁剪，`ErrTokenInvalid` 仅 `pkg/push`）：

| 哨兵 | 含义 | 典型处理 |
|------|------|----------|
| `ErrInvalidConfig` | 驱动配置缺失/非法 | 启动失败，进程退出 |
| `ErrUnknownDriver` | 未注册驱动 | 检查 blank-import |
| `ErrInvalidMessage` | 消息结构/长度非法 | 调用方 Bug，拒绝发送 |
| `ErrInvalidRecipient` | 手机号/邮箱/Token 格式非法 | 不重试；Push 可标记 Token 失效 |
| `ErrRejected` | 策略或厂商永久拒绝（裸内容未开启、模板/内容/黑名单） | 不重试；记录并告警/人工处理 |
| `ErrTokenInvalid` | 推送 Token 已失效（APNs 410、FCM UNREGISTERED） | 不重试；停用 Token |
| `ErrNotSupported` | 驱动不支持该能力（如批量） | 降级为逐条发送 |
| `ErrClosed` | 已关闭 | 生命周期 Bug |

**语义码映射**（驱动内部统一）：

| 场景 | `pkg/errors` 语义码 | 可重试 | 备注 |
|------|---------------------|--------|------|
| 参数/收件人非法 | `InvalidArgument` | 否 | 发送前校验，不触网 |
| 模板/签名不存在、内容被拒 | `FailedPrecondition` | 否 | 附 `ErrRejected` 便于 `errors.Is` |
| 密钥错误、无权限 | `PermissionDenied` / `Unauthenticated` | 否 | 立即告警，多半是配置事故 |
| 厂商限流/配额 | `ResourceExhausted` | 是 | 填充 `RetryAfter`，尊重 `Retry-After` |
| 网络错误、5xx、连接重置 | `Unavailable` | 是 | 交给 `pkg/retry` |
| `ctx` 取消/超时 | `DeadlineExceeded` / `Canceled` | 是（超时）/ 否（取消） | 取消不得重试 |
| 厂商内部错误/未知 | `Internal` | 否 | 记录原始码并告警 |
| Token 失效 | `FailedPrecondition` + `ErrTokenInvalid` | 否 | 触发 Token 清理 |

需要同时保留「哨兵 + 语义码」时，用多重 `%w` 包装（Go 1.20+）：

```go
err := fmt.Errorf("%w: %w", ErrRejected, errors.FailedPrecondition("sms: template rejected"))
```

> 业务数字码（如 `40000` / `42900`）由 `internal/shared/apperr` 在 transport 层映射；`pkg` 不感知业务码（API-04）。

### 3.3 回执与结果

```go
// Receipt 是一次「厂商已受理」的凭据；不代表最终送达。
type Receipt struct {
	Provider   string    // 驱动名，如 "aliyun"
	MessageID  string    // 厂商消息 ID；SMTP 可为空
	AcceptedAt time.Time // 使用注入时钟
}
```

- 「受理」与「送达」分离；送达回执属于异步回调（M5）。
- 批量发送返回**逐条结果**，禁止用单个 `error` 掩盖部分失败：

```go
// BatchResult 与输入等长；Errors[i] 为 nil 表示第 i 条已受理。
type BatchResult struct {
	Receipts []Receipt
	Errors   []error
}

// Failed 返回失败条数。
func (r BatchResult) Failed() int
```

### 3.4 观测钩子

```go
// Event 是一次发送尝试的只读快照；禁止包含收件人、正文、验证码与凭据。
type Event struct {
	Channel    string        // sms / email / push
	Provider   string
	Operation  string        // send / send_batch
	Template   string        // 低基数模板键，禁止拼接参数
	Result     string        // success / failure
	Code       string        // 语义码，如 unavailable
	RetryAfter time.Duration
	Duration   time.Duration
}

// Observer 由 platform 实现（日志 / 指标 / Trace）；实现必须并发安全且快速返回。
type Observer interface {
	Observe(ctx context.Context, ev Event)
}
```

`Event` 不携带 `error`，避免把底层错误对象泄漏到观测层；需要排障时由调用方用返回值处理。

### 3.5 Config 形态

三包各自定义 `Config`，字段同名同义，驱动段独立；`internal/platform` 负责 YAML → `Config` 的映射（`pkg` 不读配置源）。

```go
// Config 是 Open 的统一配置；零值不可用，Driver 必填。
type Config struct {
	Driver   string        // 驱动名，见 Drivers()
	Timeout  time.Duration // 单次发送兜底超时；0 使用驱动默认
	Observer Observer      // 可空
	Clock    clock.Clock   // 可空，默认 clock.RealClock()

	// 仅 HTTP 驱动使用；nil 时按 Timeout 新建，禁止 http.DefaultClient。
	HTTPClient *httpclient.Client

	// 驱动段：按 Driver 读取，未选中的段忽略。
	Aliyun  AliyunConfig
	Tencent TencentConfig
	Huawei  HuaweiConfig
}
```

`email` 将 `Aliyun/Tencent/Huawei` 换成 `SMTP` / `SES`，`push` 换成 `APNS` / `FCM`；公共字段保持一致，便于 platform 统一装配与测试。

### 3.6 驱动注册表

与 `pkg/storage` 保持一致：

```go
// Opener 由驱动包 init 注册。
type Opener func(ctx context.Context, cfg Config) (Sender, error)

// Register 注册驱动；同名后注册覆盖先注册（仅测试使用）。
func Register(name string, open Opener)

// Open 按 cfg.Driver 构造 Sender；未知驱动返回 ErrUnknownDriver。
func Open(ctx context.Context, cfg Config) (Sender, error)

// Drivers 返回已注册驱动名快照，用于启动日志与排障。
func Drivers() []string
```

装配示例：

```go
import (
	_ "github.com/admin-frame/admin-frame/pkg/sms/aliyun"
)

sender, err := sms.Open(ctx, sms.Config{
	Driver:  "aliyun",
	Timeout: 3 * time.Second,
	Aliyun:  sms.AliyunConfig{Region: "cn-hangzhou", SignName: "某某科技"},
})
```

---

## 4. `pkg/sms`：短信

### 4.1 契约

```go
// Sender 发送单条短信；实现必须并发安全。
type Sender interface {
	// Provider 返回驱动名，用于日志/指标。
	Provider() string
	// Send 发送一条短信；成功表示厂商已受理，不代表已送达。
	Send(ctx context.Context, msg Message) (Receipt, error)
	// Close 释放资源；幂等。
	Close() error
}

// Message 是一条已渲染的短信；模板与裸内容二选一。
type Message struct {
	// Phone 为 E.164 格式（如 +8613800138000）；驱动负责转换为厂商格式。
	Phone string
	// SignName 为已报备的签名；国内厂商必填。
	SignName string
	// TemplateID 为厂商模板编码；与 Content 互斥。
	TemplateID string
	// Params 为模板变量；键名/取值由模板定义，禁止 CR/LF 与控制字符。
	Params map[string]string
	// Content 为裸内容，仅当 Config.AllowRawContent=true 时允许。
	Content string
	// Locale 为语言/地区提示（如 zh-CN），用于国际短信路由。
	Locale string
	// IdempotencyKey 由调用方提供，用于编排层去重与排障关联。
	IdempotencyKey string
	// Reference 为业务引用（如 order_id），只允许低基数、非 PII。
	Reference string
}
```

可选能力（按需探测，避免接口膨胀）：

```go
// BatchSender 支持批量发送的驱动。
type BatchSender interface {
	SendBatch(ctx context.Context, msgs []Message) (BatchResult, error)
}

// AsBatchSender 探测批量能力。
func AsBatchSender(s Sender) (BatchSender, bool)
```

### 4.2 校验规则（发送前，不触网）

| 字段 | 规则 | 失败错误 |
|------|------|----------|
| `Phone` | 必填；`+` 开头，3–15 位数字；拒绝空格与分隔符 | `ErrInvalidRecipient` |
| `SignName` | 国内驱动必填；长度 ≤ 32；禁止控制字符 | `ErrInvalidMessage` |
| `TemplateID` / `Content` | 必须二选一；`Content` 需 `AllowRawContent` | `ErrInvalidMessage` |
| `Params` | ≤ 20 个键；键 ≤ 32 字节；值 ≤ 100 字节且禁止 CR/LF；总大小 ≤ 1 KiB | `ErrInvalidMessage` |
| `Content` | ≤ 500 字符；禁止 CR/LF 与不可见控制字符 | `ErrInvalidMessage` |
| `Locale` | 非空时须匹配 `^[a-z]{2}(-[A-Z]{2})?$` | `ErrInvalidMessage` |
| `IdempotencyKey` | ≤ 128 字节；建议 `sms:<scene>:<biz_id>` | `ErrInvalidMessage` |

> `Message.Validate` 只做结构化校验；`Content` 是否可用由驱动在 `Send` 开始时依据 `Config.AllowRawContent` 判定，未开启时返回 `ErrRejected`。模板参数可能被厂商直接代入短信正文，CR/LF 会破坏签名/内容结构，必须在 `Validate` 阶段拒绝。

### 4.3 驱动矩阵

| 驱动 | 协议/API | 鉴权 | 关键能力 | 建议阶段 |
|------|----------|------|----------|----------|
| `memory` | 进程内 | 无 | 只记录、不出网；本地/单测 | M0 |
| `aliyun` | Dysmsapi `SendSms`（RPC 风格 HTTP） | AccessKey + HMAC-SHA1 签名 | 国内短信、模板、`OutId` | M2 |
| `tencent` | `sms.SendSms`（2021-01-11） | TC3-HMAC-SHA256 | 国内/国际、模板、批量 | M5 |
| `huawei` | MSGSMS | WSSE AK/SK | 国内短信 | M5 |

L1 实现要点：

- 请求构造使用 `net/url` + `pkg/httpclient`，禁止字符串拼接查询串（防注入、防签名错位）。
- 签名算法在驱动内用标准库实现（`crypto/hmac`、`crypto/sha1`、`crypto/sha256`），用官方文档示例做 golden test。
- 所有请求参数先经 `url.Values.Encode()` 再签名；时间戳使用注入时钟，签名测试固定时间。
- `OutId` 透传 `IdempotencyKey`（阿里云长度 ≤ 128，需截断并保持稳定哈希），便于厂商侧排障。
- 不引入官方 SDK；若后续需要批量/回执等能力且 HTTP 不可达，再按 D4 增加 L2 驱动。

### 4.4 厂商错误映射

| 厂商 | 原始码示例 | 语义码 | 可重试 |
|------|------------|--------|--------|
| 阿里云 | `isv.BUSINESS_LIMIT_CONTROL` | `ResourceExhausted` | 是 |
| 阿里云 | `isv.MOBILE_NUMBER_ILLEGAL` | `InvalidArgument` | 否 |
| 阿里云 | `isv.SMS_TEMPLATE_ILLEGAL` / `isv.SMS_SIGNATURE_ILLEGAL` | `FailedPrecondition` + `ErrRejected` | 否 |
| 阿里云 | `isv.AMOUNT_NOT_ENOUGH` | `PermissionDenied` | 否（告警：余额） |
| 腾讯云 | `LimitExceeded.PhoneNumberDailyLimit` | `ResourceExhausted` | 是 |
| 腾讯云 | `InvalidParameterValue.TemplateNotMatch` | `FailedPrecondition` | 否 |
| 腾讯云 | `AuthFailure.*` | `PermissionDenied` | 否 |
| 通用 | HTTP 5xx / 连接错误 / 读超时 | `Unavailable` | 是 |

驱动必须保留原始码到 `ProviderError.Code`（仅日志/错误详情），对外只暴露语义码。

### 4.5 非目标（本期）

- 短信上行/回复解析、退订关键词处理（业务层）。
- 送达回执回调（M5；届时新增 `Reporter`，签名校验用 `pkg/crypto.HMACSHA256Verify`）。
- 国际短信路由与多国家签名管理。

---

## 5. `pkg/email`：邮件

### 5.1 契约

```go
// Sender 发送单封邮件；实现必须并发安全。
type Sender interface {
	Provider() string
	Send(ctx context.Context, msg Message) (Receipt, error)
	Close() error
}

// Address 是 RFC 5322 地址；Name 可为 UTF-8，Email 必须为 ASCII。
type Address struct {
	Name  string
	Email string
}

// Attachment 是内存中的附件；大文件应由业务改用对象存储链接。
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message 是一封待发送邮件。
type Message struct {
	From        Address
	To          []Address
	Cc          []Address
	Bcc         []Address
	ReplyTo     *Address
	Subject     string
	Text        string            // 纯文本正文；与 HTML 至少一个
	HTML        string            // HTML 正文；调用方负责转义用户输入
	Headers     map[string]string // 仅允许白名单 Header，见 5.2
	Attachments []Attachment
	IdempotencyKey string
	Reference      string
}
```

### 5.2 校验与 MIME 构造规则

| 项 | 规则 | 失败错误 |
|----|------|----------|
| `From` | 地址 ASCII；显示名不得含 CR/LF；为空时使用 `Config.From`，二者皆空则拒绝 | `ErrInvalidMessage` |
| `To` | 1–50 个；地址 ASCII；去重后不得为空 | `ErrInvalidRecipient` |
| `Cc/Bcc` | 合计 ≤ 50；Bcc 不写入 Header | `ErrInvalidMessage` |
| `Subject` | ≤ 200 字符；禁止 CR/LF | `ErrInvalidMessage` |
| 正文 | `Text` 与 `HTML` 至少一个 | `ErrInvalidMessage` |
| `Headers` | 键仅允许白名单：`X-Entity-Ref-ID`、`List-Unsubscribe`、`List-Unsubscribe-Post`、`X-Priority` 等；键/值禁止 CR/LF | `ErrInvalidMessage` |
| 附件 | ≤ 10 个；单个 ≤ 5 MiB；总邮件 ≤ 10 MiB；文件名禁止路径分隔符/CR/LF | `ErrInvalidMessage` |
| 可执行附件 | 默认拒绝 `.exe/.bat/.cmd/.js/.vbs/.scr/.ps1`（可配置关闭） | `ErrRejected` |

MIME 结构（标准库 `mime/multipart` 构造）：

```text
multipart/mixed
├── multipart/alternative
│   ├── text/plain; charset=UTF-8
│   └── text/html; charset=UTF-8
└── attachment 1..n（含 Content-Type / Content-Disposition / RFC 2231 文件名）
```

- `Subject` 使用 `mime.QEncoding` 编码非 ASCII。
- `Date` 使用注入时钟并转 UTC；`Message-ID` 域名取 `From.Email` 的域。
- 正文使用 quoted-printable 或 base64；确保行长 ≤ 78，避免被 MTA 折行破坏。
- `Bcc` 仅用于 SMTP 信封，绝不写入消息头。

### 5.3 SMTP 驱动（M1）

| 配置项 | 说明 | 默认 |
|--------|------|------|
| `host` / `port` | SMTP 服务器 | 必填 / `587` |
| `encryption` | `starttls` / `tls` / `none` | `starttls`；生产禁止 `none` |
| `username` / `password` | 认证凭据；密码只来自 env/KMS | 按需 |
| `auth` | `plain` / `login` / `none` | `plain` |
| `helo_name` | EHLO 名称 | 主机名 |
| `from` | 默认发件人；`Message.From` 为空时使用 | 空 |
| `timeout` | 单次发送上限 | `10s` |
| `tls_min_version` | TLS 最低版本 | `1.2` |
| `max_message_bytes` | 单封邮件上限 | `10 MiB` |
| `max_recipients` | 单封收件人上限 | `50` |

实现约束：

1. 使用 `net.Dialer.DialContext` 建连，连接 deadline 取 `min(ctx deadline, now+Timeout)`；`ctx` 取消时关闭连接以中断阻塞读写（`net/smtp` 是 frozen API，不支持 ctx，需显式说明该限制）。
2. `starttls` 必须校验证书与主机名；禁止 `InsecureSkipVerify`（配置校验直接拒绝）。
3. 认证只在 TLS 之上进行；`none` 仅允许 `env=local` 且显式开启。
4. 每次发送独立连接，不做连接池（M1）；高吞吐场景用本地 MTA/HTTP API，避免自研池化。
5. 4xx 视为可重试（`Unavailable`），5xx 视为永久（`FailedPrecondition`），`535` 认证失败映射 `PermissionDenied`。
6. 驱动内不重试；由编排层用 `pkg/retry` 包裹（D2）。

### 5.4 可达性与安全

- SPF / DKIM / DMARC、IP 预热、退信处理由 MTA 或邮件服务商承担；应用层**不**自签 DKIM（如确需，L2 引入 `go-mail` 并走 DEP-06）。
- 发件域与 `From` 域必须一致；跨域 `From` 默认拒绝（防伪造）。
- HTML 正文由调用方模板渲染；`pkg/email` 不做 HTML 转义（职责边界），但文档与示例必须强调 `html/template`。
- 禁止在日志中打印完整邮件正文与收件人列表；错误只带语义码与 `Message-ID`。

### 5.5 后续驱动

| 驱动 | 协议 | 场景 | 阶段 |
|------|------|------|------|
| `ses` | AWS SES v2 HTTP API（SigV4） | 高吞吐、托管退信 | 按需 |
| `sendgrid` | HTTP API | 海外营销邮件 | 按需 |

---

## 6. `pkg/push`：推送

### 6.1 契约

```go
// Platform 是推送目标平台；一个驱动只服务一个平台。
type Platform string

const (
	// PlatformIOS 表示 Apple Push Notification service。
	PlatformIOS Platform = "ios"
	// PlatformAndroid 表示 Android 推送（FCM/国内厂商）。
	PlatformAndroid Platform = "android"
	// PlatformWeb 表示 Web Push（后续）。
	PlatformWeb Platform = "web"
)

// Sender 向单个设备 Token 推送；实现必须并发安全。
type Sender interface {
	Provider() string
	Platform() Platform
	Send(ctx context.Context, msg Message) (Receipt, error)
	Close() error
}

// Priority 是投递优先级；映射到 APNs/FCM 对应字段。
type Priority string

const (
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
)

// Message 是一条推送消息。
type Message struct {
	// Token 为设备 Token；禁止日志输出。
	Token string
	Title string
	Body  string
	// Data 为自定义键值；键 ≤ 64、值 ≤ 512 字节，总大小受平台上限约束。
	Data map[string]string
	// Badge 为 iOS 角标；nil 表示不修改。
	Badge *int
	// Sound 为声音名；空表示默认，"-" 视平台语义处理。
	Sound string
	// TTL 为消息有效期；0 表示平台默认。
	TTL time.Duration
	// CollapseKey 用于折叠同类消息（APNs collapse-id / FCM collapse_key）。
	CollapseKey string
	Priority    Priority
	// IdempotencyKey 用于编排层去重与 apns-id 生成。
	IdempotencyKey string
	Reference      string
}
```

### 6.2 校验规则

| 项 | 规则 |
|----|------|
| `Token` | 必填；长度 32–4096；仅允许 `[A-Za-z0-9_\-:.=]`（APNs/FCM Token 字符集） |
| `Title` / `Body` | 至少一个非空；总 payload 编码后 ≤ 4 KiB（APNs/FCM 上限） |
| `Data` | 键不得与保留字段冲突（`aps` / `notification` / `data`）；总大小 ≤ 2 KiB |
| `Badge` | 0–99999 |
| `TTL` | `[0, 7*24h]`；0 表示默认 |
| `Priority` | 仅 `normal` / `high` |

Payload 超限在发送前返回 `ErrInvalidMessage`，不要依赖厂商 413。

### 6.3 APNs 驱动（M3）

| 配置项 | 说明 |
|--------|------|
| `endpoint` | `https://api.push.apple.com`（生产）/ `https://api.sandbox.push.apple.com` |
| `topic` | App Bundle ID |
| `key_id` / `team_id` | Token-based 认证 |
| `private_key` | `.p8` 内容或文件路径；只来自 env/KMS |
| `push_type` | 默认 `alert`；静默推送用 `background` |
| `timeout` | 默认 `5s` |

实现要点：

1. 使用 HTTP/2（标准库 `net/http` 自动协商）+ `pkg/httpclient`；每个 Token 一个请求，HTTP/2 多路复用。
2. ES256 JWT：`crypto/ecdsa` + `crypto/x509` 解析 P-256 私钥；Header `{alg:"ES256", kid}`，Claims `{iss:teamID, iat}`。
3. Token 缓存：`iat` 后 50 分钟刷新（Apple 要求 20–60 分钟），使用 `clock.Clock` 计算过期，缓存用互斥锁保护。
4. `IdempotencyKey` 若为 UUID 则用作 `apns-id`；非 UUID 时由编排层生成并记录映射。
5. 错误映射：

| HTTP / reason | 语义码 | 额外 |
|---------------|--------|------|
| 200 | 成功 | `Receipt.MessageID` = `apns-id` |
| 400 `BadDeviceToken` / `DeviceTokenNotForTopic` | `InvalidArgument` + `ErrTokenInvalid` | 停用 Token |
| 400 `BadCollapseId` / `PayloadTooLarge` | `InvalidArgument` | 调用方修正 |
| 403 `ExpiredProviderToken` | `Unavailable`（可重试） | 驱动清空 JWT 缓存后返回；上层最多重试一次 |
| 403 `InvalidProviderToken` | `PermissionDenied` | `key_id/team_id/private_key` 配置错误，告警 |
| 404 `BadPath` | `Internal` | 配置错误，告警 |
| 410 `Unregistered` | `FailedPrecondition` + `ErrTokenInvalid` | 停用 Token |
| 429 `TooManyRequests` | `ResourceExhausted` | 读 `Retry-After` |
| 500 / 503 | `Unavailable` | 可重试 |

### 6.4 FCM 驱动（M3）

| 配置项 | 说明 |
|--------|------|
| `project_id` | Firebase 项目 ID |
| `credentials_json` | Service Account JSON 内容或路径；只来自 env/KMS |
| `endpoint` | 默认 `https://fcm.googleapis.com` |
| `timeout` | 默认 `5s` |

实现要点：

1. OAuth2：Service Account JWT（RS256）→ `https://oauth2.googleapis.com/token`，scope `https://www.googleapis.com/auth/firebase.messaging`；access token 缓存至 `expires_in - 60s`（用 `clock.Clock`）。
2. 发送：`POST /v1/projects/{project}/messages:send`，Body 为 FCM HTTP v1 结构；Data 值必须为字符串。
3. 错误映射：

| FCM 状态 / reason | 语义码 | 额外 |
|-------------------|--------|------|
| 200 | 成功 | `Receipt.MessageID` = `name` |
| 400 `INVALID_ARGUMENT` | `InvalidArgument` | 参数/Token 格式 |
| 401 / 403 `SENDER_ID_MISMATCH` | `PermissionDenied` | 检查项目/凭据 |
| 404 `UNREGISTERED` | `FailedPrecondition` + `ErrTokenInvalid` | 停用 Token |
| 429 `QUOTA_EXCEEDED` / `SENDER_QUOTA_EXCEEDED` | `ResourceExhausted` | 读 `RetryInfo.retryDelay` |
| 500 `INTERNAL` | `Unavailable` | Google 语义为服务端内部错误，可重试并告警 |
| 503 `UNAVAILABLE` | `Unavailable` | 可重试 |

4. 国内 Android 设备通常无 GMS，FCM 不可达；M5 接华为 HMS / 小米 / OPPO / vivo，由 `internal/notify` 按设备厂商路由，`pkg/push` 只提供驱动。

### 6.5 Token 生命周期

- `pkg/push` **不**持久化 Token，只返回 `ErrTokenInvalid`。
- `internal/notify` 负责停用、重绑与清理；Token 落库需加密（`pkg/crypto`），日志用 `pkg/mask.Token`。
- 卸载/重装会产生 Token 轮换；同一用户多设备由业务层维护，`pkg` 不感知。

---

## 7. 横切能力接入

| 能力 | 实现位置 | 说明 |
|------|----------|------|
| 超时 | 驱动 + 调用方 ctx | 驱动 `Timeout` 是兜底；调用方 deadline 必须更短（REL-01 超时分层） |
| 重试 | `internal/notify` | `pkg/retry`，`RetryIf = errors.RetryableOf`，最多 2–3 次；尊重 `RetryAfter` |
| 熔断 | `internal/notify` | 每供应商一个 `pkg/circuitbreaker`；`OnStateChange` 接指标/告警 |
| 限流 | `internal/notify` | `pkg/ratelimit`：供应商 QPS + 收件人频控 |
| 幂等/去重 | `internal/notify` | Redis `SETNX` + TTL；键 `notify:<channel>:<idempotency_key>` |
| 主备切换 | `internal/notify` | 仅对可重试错误切换备用供应商；永久错误不切换 |
| 降级 | `internal/notify` | 必须打点 `degraded=true`（REL-06），禁止静默 |

### 7.1 重试与熔断顺序

推荐**熔断包住重试**（每次重试都经过熔断器判断）：

```text
调用方
  └─ circuitbreaker.Do
       └─ retry.Do
            └─ driver.Send（单次尝试）
```

理由：供应商整体不可用时快速失败，避免重试风暴；半开探测也经过重试预算。禁止在驱动内部再重试。

**约束**：驱动构造 `pkg/httpclient` 时必须设置 `DisableRetry: true`，且不得开启 `RetryNonIdempotent`；否则会与编排层重试叠加，并可能重复发送非幂等请求。

### 7.2 就绪与降级

- 通知渠道是**尽力而为**能力：供应商不可用**不得**导致 `/readyz` 失败或阻塞主业务（OBS-03 的「关键依赖」不包含短信/邮件/推送）。
- 就绪信息仅通过 `notify_breaker_state` 与告警体现；`/readyz` 最多在调试端点展示「已配置驱动名」。
- 降级必须显式：记录 `degraded=true` 与语义码（REL-06），禁止 `return nil` 掩盖失败。

### 7.3 频控键（防 PII 落 Redis）

```text
rate:sms:provider:<provider>
rate:sms:phone:<hex(HMAC-SHA256(phone, salt))>
rate:sms:ip:<ip>
```

- 手机号/邮箱必须经带盐 HMAC 后再作为键，禁止明文（防 Redis 泄露导致 PII 扩散）；salt 来自 env/KMS，按环境独立。
- 频控与配额拒绝统一映射 `apperr.TooManyRequests`（`42900`），响应文案笼统，不暴露剩余额度。
- 建议额度（需产品确认）：同一手机号 `1/分钟`、`5/小时`、`10/天`；同 IP `20/小时`。
- 验证码场景：发送前必须过人机校验；发送接口对外始终返回相同文案，避免手机号枚举。

---

## 8. 配置草案

> 配置由 `internal/notification/config` 定义，`cmd/internal/notificationruntime` 在字典装配期
> 读取凭据并构造驱动；`pkg` 不读文件、不读环境变量（CFG-07）。

```yaml
notification:
  channels:
    email:
      driver: smtp                # memory | smtp | disabled
      from: no-reply@example.com
      from_name: AdminFrame
      smtp:
        host: smtp.example.com
        port: 587
        encryption: starttls      # starttls（默认）| tls
        auth: plain               # plain（默认）| login
        username: no-reply@example.com
        password_env: ADMIN_FRAME_NOTIFICATION_SMTP_PASSWORD
        tls_min_version: "1.2"
        allow_insecure: false     # 仅 local 允许 true

    sms:
      driver: aliyun              # memory | aliyun | disabled
      template_id: SMS_123
      sign_name: "某某科技"
      aliyun:
        endpoint: https://dysmsapi.aliyuncs.com
        region: cn-hangzhou
        access_key_id: ""
        access_key_secret_env: ALIYUN_SMS_ACCESS_KEY_SECRET
        sign_name: ""

    push:
      driver: apns                # memory | apns | fcm | disabled
      platform: ios               # apns→ios；fcm→android
      apns:
        endpoint: https://api.push.apple.com
        topic: com.example.app
        key_id: "ABC123DEFG"
        team_id: "TEAM123456"
        private_key_env: ADMIN_FRAME_NOTIFICATION_APNS_PRIVATE_KEY
        push_type: alert
      fcm:
        project_id: "example-prod"
        endpoint: https://fcm.googleapis.com
        credentials_json_env: ADMIN_FRAME_NOTIFICATION_FCM_CREDENTIALS
```

环境变量（只用于注入**值**；YAML 里只写变量名）：

| 变量 | 说明 |
|------|------|
| `ADMIN_FRAME_NOTIFICATION_SMTP_PASSWORD` | SMTP 密码/授权码 |
| `ALIYUN_SMS_ACCESS_KEY_SECRET` | 阿里云短信 AK Secret（名称可改，与 `*_env` 一致即可） |
| `ADMIN_FRAME_NOTIFICATION_APNS_PRIVATE_KEY` | APNs `.p8` 内容（PEM）或文件路径 |
| `ADMIN_FRAME_NOTIFICATION_FCM_CREDENTIALS` | Service Account JSON 或文件路径 |

**校验规则（启动期，失败即退出）**：

1. `driver` 必须是 `memory | smtp | aliyun | apns | fcm | disabled` 之一；未配置视为 memory。
2. `env=prod/staging` 禁止 `memory`；未使用的渠道必须显式 `disabled`，避免静默丢弃。
3. `email.smtp`：`from` / `host` / `password_env` 必填；prod/staging 禁止 `encryption=none` 与 `allow_insecure=true`。
4. `sms.aliyun`：`template_id`、`sign_name`（或 `aliyun.sign_name`）、`access_key_id`、`access_key_secret_env` 必填。
5. `push.apns`：`topic` / `key_id` / `team_id` / `private_key_env` 必填且 `platform=ios`；`push.fcm`：`credentials_json_env` 必填且 `platform=android`。
6. 凭据值在装配期从环境变量读取；变量缺失时进程失败关闭，不允许「启动成功但永远发不出去」。
7. `pkg/email.Config` / `pkg/sms.Config` / `pkg/push.Config` 仍会二次校验连接参数与 TLS 下限。

---

## 9. 可观测性

### 9.1 指标（由 platform 的 `Observer` 实现）

| 指标 | 类型 | 标签 | 说明 |
|------|------|------|------|
| `notify_send_total` | Counter | `channel`、`provider`、`result`、`code` | `code` 只允许语义码（低基数） |
| `notify_send_duration_seconds` | Histogram | `channel`、`provider`、`result` | 分位数告警 |
| `notify_retry_total` | Counter | `channel`、`provider` | 重试次数 |
| `notify_rate_limited_total` | Counter | `channel`、`scope`（provider/recipient/ip） | 频控拒绝 |
| `notify_breaker_state` | Gauge | `channel`、`provider` | 0=Closed，1=HalfOpen，2=Open |
| `notify_delivery_total` | Counter | `channel`、`provider`、`status` | 仅 M5 回执接入后 |
| `notify_token_invalid_total` | Counter | `provider` | Token 失效清理量 |

**禁止**：手机号、邮箱、Token、模板参数、供应商原始错误码作为标签（高基数/PII）。

### 9.2 日志

由 platform 统一输出，字段固定：

| 字段 | 示例 | 说明 |
|------|------|------|
| `notify_channel` | `sms` | 渠道 |
| `notify_provider` | `aliyun` | 驱动 |
| `notify_operation` | `send` | 操作 |
| `notify_template` | `login_code` | 低基数模板键 |
| `notify_result` | `failure` | 结果 |
| `notify_code` | `unavailable` | 语义码 |
| `notify_message_id` | `...` | 厂商消息 ID |
| `duration_ms` | `123` | 耗时 |
| `recipient` | `138****8000` | 默认不记录；排障需显式开启，且必须经 `pkg/mask.Phone` / `pkg/mask.Email` |
| `trace_id` | `...` | 与入口一致（OBS-01/05） |

**禁止**记录：正文、验证码、完整 Token、完整邮箱/手机号、`Authorization`、AK/SK。

### 9.3 Trace

- 编排层创建 `notify.send` span，属性：`notify.channel`、`notify.provider`、`notify.template`、`notify.result`。
- 出站 HTTP 由 `pkg/httpclient` 自动注入 W3C `traceparent`；SMTP 无自动注入，依赖 span 包裹。
- 错误只记录语义码与事件，不把厂商响应体整体写入 span。

### 9.4 告警建议

| 告警 | 条件 | 级别 |
|------|------|------|
| 发送成功率下降 | 5 分钟成功率 < 95% | P1 |
| 供应商熔断 | `notify_breaker_state==2` 持续 1 分钟 | P1 |
| 认证失败 | `code=permission_denied` 连续 3 次 | P1（配置事故） |
| 限流激增 | `notify_rate_limited_total` 突增 5 倍 | P2 |
| Token 失效率 | Android/iOS 失效率 > 10% | P2（检查证书/项目配置） |

---

## 10. 安全、合规与多租户

| 风险 | 控制措施 | 关联 |
|------|----------|------|
| 密钥泄漏 | 只从 env/KMS/挂载文件读取；YAML 不含密钥；CI gitleaks | SEC-01、CFG-06 |
| PII 入日志/Trace | 统一 `pkg/mask`；错误与 `Event` 不含收件人；日志脱敏测试 | SEC-05、C-08 |
| Header 注入 | Subject/Address/自定义 Header/附件名禁止 CR/LF；白名单 | 本文 5.2 |
| 短信内容注入 | 模板参数禁用 CR/LF；长度与总量限制；裸内容默认关闭 | 本文 4.2 |
| 短信轰炸/枚举 | 人机校验（入口）+ 手机号/IP 频控 + 统一响应文案 | REL-05、本文 7.3 |
| 邮件伪造 | `From` 域校验；SPF/DKIM/DMARC 由 MTA 负责；禁止 `InsecureSkipVerify` | 本文 5.4 |
| Token 泄漏 | 不落日志；落库加密；错误只返回 `ErrTokenInvalid` | SEC-05 |
| SSRF | Endpoint 只来自配置，不接受用户输入；HTTP 客户端统一 `pkg/httpclient` | SEC-02 |
| 厂商回调伪造 | HMAC 校验 + 时间窗 + 幂等更新（M5） | `pkg/crypto.HMACSHA256Verify` |
| 多租户越权 | `Reference` 仅作排障；鉴权与 `merchant_id` 约束在 `internal/notify` / app | SEC-14 |
| 审计 | A/B 发送记录模板、操作者、收件人数；C 端验证码只记指标不记号码 | OBS-07、AUDIT |

**合规提示（需法务/运营确认）**：

- 国内短信签名与模板必须报备；营销短信需退订方式与发送时段限制。
- 验证码有效期建议 5 分钟、最多校验 5 次、发送后 60 秒内不可重发。
- 邮件营销需提供 `List-Unsubscribe`；退订状态由 `internal/notify` 持久化。
- 数据保留：发送记录默认 30–90 天；正文不持久化。

---

## 11. 测试策略

| 层级 | 内容 | 门禁 |
|------|------|------|
| 单测 | 消息校验、长度/字符集边界、错误映射、配置校验 | `go test ./pkg/sms/... ./pkg/email/... ./pkg/push/...` |
| 契约测 | `smstest.Run` / `emailtest.Run` / `pushtest.Run` 对所有驱动跑同一套用例 | 覆盖率 ≥ 85%（语句） |
| Golden | 阿里云签名串、MIME 输出、APNs/FCM 请求 JSON | 变更需更新 golden 并说明 |
| HTTP fake | `httptest.Server` 模拟厂商状态码与错误体 | CI 必跑，不出网 |
| SMTP fake | 进程内最小 SMTP server（`net.Listen`）验证握手、认证、DATA | CI 必跑 |
| 安全用例 | CRLF 注入、超长 payload、错误消息不含 PII、Token 脱敏 | CI 必跑 |
| 并发/取消 | `go test -race`；pre-canceled ctx 不触网 | TEST-04 |
| 集成（可选） | 沙箱凭据 nightly；禁止生产凭据（TEST-05） | 非阻断 |

契约测试形态（与 `pkg/storage/storagetest` 对齐）：

```go
// smstest.Run 对任意 Sender 执行统一契约测试。
//
// newSender 必须返回可用实例；测试结束后会调用 Close。
func Run(t *testing.T, newSender func(t *testing.T) sms.Sender)
```

必测项：

1. 合法消息返回非空 `Receipt.Provider` 与 `AcceptedAt`。
2. 非法手机号返回 `ErrInvalidRecipient`，且 **0 次**网络调用（fake 计数断言）。
3. 超长/含 CRLF 参数返回 `ErrInvalidMessage`。
4. pre-canceled `ctx` 返回 `DeadlineExceeded`/`Canceled`，不触网。
5. `Close` 后 `Send` 返回 `ErrClosed`；重复 `Close` 无错误。
6. 并发 100 次 `Send` 无 race、无 panic。
7. 错误字符串不包含手机号/邮箱/Token/正文。

---

## 12. 分期落地

> 估算按单人开发、含测试与 Review；不含厂商账号申请与模板报备时间。

| 阶段 | 交付 | 关键文件 | 验收 | 估算 |
|------|------|----------|------|------|
| **M0** 契约与骨架 | 三包契约、校验、错误、注册表、memory 驱动、契约测试；本文档 | `pkg/sms/{doc,sender,message,errors,config,driver,memory}.go` + `smstest/`；email/push 同构 | `go test ./pkg/...`；无新增依赖；`go vet` / lint 通过 | 0.5–1 天 |
| **M1** 邮件 SMTP | MIME 构造、Header 白名单、附件、SMTP 驱动、fake SMTP 测试 | `pkg/email/message.go`、`mime.go`、`smtp/`、`emailtest/` | 本地 SMTP 收信可解析；注入/超限用例通过；覆盖率 ≥ 85% | 1.5–2 天 |
| **M2** 短信（阿里云） | 签名、请求、错误映射、`OutId`、httptest golden；调用方接入最小频控/去重（可暂放 app，M4 收敛） | `pkg/sms/aliyun/`、`pkg/sms/smstest/`；调用方入口频控 | 沙箱发送成功；限流/非法号码/模板错误映射正确；PII 用例通过 | 2–3 天 |
| **M3** 推送（APNs + FCM） | ES256/RS256 JWT、Token 缓存、HTTP/2、错误映射、Token 失效语义 | `pkg/push/apns/`、`pkg/push/fcm/`、`pushtest/` | httptest 验证 JWT 与 payload；410/UNREGISTERED 触发清理；`clock.Manual` 可测过期 | 2–3 天 |
| **M4** 业务编排 | 模板中心、渠道路由、去重、退订、发送记录、回执回调、指标/告警 | `internal/notify/*`、`internal/platform` 装配、`configs/*.yaml.example` | 首个业务场景上线；`/readyz` 不依赖外部渠道；降级可观测 | 3–5 天 |
| **M5** 增强 | 批量、腾讯云/华为云、SES、国内 Push、送达回执、主备切换 | 各驱动子包 + `internal/notify` | 主备演练通过；回执签名校验通过 | 按需 |

### 12.1 每个里程碑的 DoD

- [ ] `go build ./...`、`go vet ./...`、`go test -race ./...`、`golangci-lint` 全绿。
- [ ] 新增直接依赖已在 MR 说明用途/许可证/替代方案（DEP-06）；默认无新增。
- [ ] `pkg` 未 import `internal/...`；驱动包未被 `pkg` 根包反向引用。
- [ ] 所有导出符号有 godoc（C-02/C-03），错误/并发/零值/边界写清（C-04）。
- [ ] 无 PII 日志；安全用例覆盖 CRLF、超长、Token 脱敏。
- [ ] 配置校验有单测；`env=prod` 禁止项有反例测试。
- [ ] 指标/日志字段与本文第 9 节一致。
- [ ] 更新 `docs/pkg-roadmap.md` 状态与本文「状态」。

---

## 13. 依赖与许可证

### 13.1 默认（L1，零新增第三方依赖）

| 依赖 | 用途 | 说明 |
|------|------|------|
| `pkg/httpclient` | 出站 HTTP、超时、Trace、重试 | 已存在；禁止 `http.DefaultClient`（REL-01） |
| `pkg/errors` | 语义码、因果链、可重试判定 | 已存在 |
| `pkg/retry` | 编排层重试 | 已存在；驱动不使用 |
| `pkg/circuitbreaker` | 供应商熔断 | 已存在 |
| `pkg/clock` | 回执时间、JWT/Token 过期 | 已存在；测试注入 `Manual` |
| `pkg/crypto` | HMAC（回执校验）、SHA256 | 已存在；厂商签名在驱动内用标准库 |
| `pkg/mask` | 日志脱敏 | 已存在 |
| `pkg/ratelimit` | 编排层频控 | 已存在；仅 `internal/notify` 使用 |
| 标准库 | `net/smtp`、`mime/*`、`crypto/tls`、`crypto/hmac`、`crypto/ecdsa`、`crypto/rsa`、`net/http` | Go 1.26 |

### 13.2 可选（L2，仅在需要厂商独有能力时引入）

| 依赖 | 用途 | 许可证（引入时复核） | 替代方案 |
|------|------|----------------------|----------|
| `github.com/aliyun/alibaba-cloud-sdk-go` | 阿里云短信原生 SDK | Apache-2.0 | L1 HTTP + 自实现签名 |
| `github.com/tencentcloud/tencentcloud-sdk-go` | 腾讯云短信原生 SDK | Apache-2.0 | L1 HTTP + TC3 签名 |
| `github.com/huaweicloud/huaweicloud-sdk-go-v3` | 华为云短信 | Apache-2.0 | L1 HTTP |
| `github.com/wneessen/go-mail` | SMTP 连接池 / DKIM | MIT | 标准库 `net/smtp` |
| `github.com/sideshow/apns2` | APNs 客户端 | MIT | 标准库 HTTP/2 + ES256 |
| `firebase.google.com/go/v4` | FCM 客户端 | Apache-2.0 | 标准库 + OAuth2 |

约束：

- SDK **只能**出现在 `pkg/{sms,email,push}/<driver>`，不得泄漏到根包。
- 引入前跑 `make vuln` / `make check-license`；`go mod tidy` 无未提交 diff（DEP-02/04/05）。
- `net/smtp` 为 frozen API，无连接池与 DKIM；如吞吐或签名需求超出，按 L2 切换并保持 `Sender` 契约不变。

---

## 14. 开放问题（需 Owner 确认）

| # | 问题 | 建议默认 | 影响 |
|---|------|----------|------|
| 1 | 首个渠道做 email 还是 sms？ | 有验证码需求则先 sms；否则 email | 里程碑顺序 |
| 2 | 是否允许裸内容短信？ | 禁止；仅模板 | 校验与合规 |
| 3 | 国内短信默认厂商？ | 阿里云（文档/生态成熟） | 驱动优先级 |
| 4 | 是否需要 App Push？ | 有 App 再开；否则仅保留契约 | M3 是否启动 |
| 5 | 邮件是否应用层 DKIM？ | 否，交给 MTA/SES | 是否引入 `go-mail` |
| 6 | 发送记录保留期？ | 90 天，正文不落库 | 存储与合规 |
| 7 | 是否需要多租户独立通道账号？ | 先单账号 + 租户签名白名单 | 配置模型 |
| 8 | 是否接入国内 Android 厂商通道？ | M5 按设备占比决定 | 推送到达率 |

---

## 15. 验收清单（启动前）

- [ ] 确认首个真实调用方与场景（验证码 / 告警 / 订单通知）。
- [ ] 完成厂商账号、签名、模板、发信域与 SPF/DKIM/DMARC 准备。
- [ ] 确认频控额度、发送时段、退订与数据保留策略。
- [ ] 确认密钥注入方式（env / KMS / 挂载文件）与轮换流程。
- [ ] 确认告警接收人与 Runbook（认证失败、熔断、成功率下降）。
- [ ] 安全 Review：PII、Header 注入、SSRF、回调验签、多租户隔离。
- [ ] 将本文状态改为「进行中」，并在 `docs/pkg-roadmap.md` 回写。

---

## 16. 参考

- 代码：`pkg/storage`（注册表 / L1-L2 驱动模式）、`pkg/httpclient`、`pkg/retry`、`pkg/circuitbreaker`、`pkg/clock`、`pkg/errors`、`pkg/mask`
- 协议：RFC 5321/5322（SMTP / 邮件）、RFC 2045–2047/2231（MIME）、APNs Provider API、FCM HTTP v1、阿里云 Dysmsapi、腾讯云 SMS API
- 规范：SEC-01/05、REL-01/05/06、OBS-03/07、CFG-04/06/07、DEP-02/04/05
