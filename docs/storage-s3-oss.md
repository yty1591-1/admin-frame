# 对象存储：多云模块化驱动规划与选型

> **状态**：已落地（M0–M5；默认 L1，`native=true` 切 L2）
> **关联**：[`pkg/storage`](../pkg/storage)、[`docs/pkg-roadmap.md`](./pkg-roadmap.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`DEPENDENCY_STANDARDS.md`](./DEPENDENCY_STANDARDS.md)、[`ARCHITECTURE.md`](./ARCHITECTURE.md) §十五
> **最后更新**：2026-11-05（补状态与变更方式字段；新增「1. 定位与边界」）
> **变更方式**：新增驱动、调整键/错误语义或默认 `driver` 须同步 `pkg/storage` 契约、配置示例与本文选型表，经安全 Review 后合入
> **目标读者**：后端 / 平台装配 / 安全 Review

---

## 1. 定位与边界

对象存储是本仓库的**模块化基础设施能力**：

- **契约统一**：业务只依赖 `pkg/storage.Store` / `Presigner`，不感知厂商。
- **驱动可插拔**：阿里云 OSS、腾讯云 COS、华为云 OBS、AWS S3、MinIO 等以**独立子包 + 注册表**接入（类比 `database/sql` 驱动模型）。
- **装配可选**：`cmd` / `platform` **按需 blank-import** 所需驱动；未引用的驱动不进入进程。
- **语义一致**：一套键规范、错误语义、前缀隔离（`Join` / `ValidateKey` / `WithPrefix`）覆盖所有后端。
- **安全默认**：大文件 **Presign 直传**；密钥走环境变量 / IAM / 云角色（SEC-01）；日志禁止完整签名串（SEC-05）。
- **本地可测**：内置 `memory` / `local`；CI 可用 MinIO。

```text
业务 / app
    │ 只用 Store
    ▼
pkg/storage          ← Store / Presigner / Open / Register
    │ blank-import
    ├── memory / local     （内置）
    ├── storage/s3         （AWS S3、MinIO、通用 S3 兼容）
    ├── storage/oss        （阿里云 OSS）
    ├── storage/cos        （腾讯云 COS）
    └── storage/obs        （华为云 OBS）
```

### 1.1 非目标

- 运行时热切换多云双写 / 自动 failover（另开 ADR）。
- CDN、图片处理、内容审核（上传后异步流水线）。
- 在领域层引用任何云 SDK。

新增 SDK 须遵守 DEP-06（用途、许可证、替代方案）。

---

## 2. 模块化模型

### 2.1 注册表（已实现方向）

```go
// 进程装配
import (
    _ "github.com/admin-frame/admin-frame/pkg/storage/s3"
    _ "github.com/admin-frame/admin-frame/pkg/storage/oss"
    _ "github.com/admin-frame/admin-frame/pkg/storage/cos"
    _ "github.com/admin-frame/admin-frame/pkg/storage/obs"
)

store, err := storage.Open(ctx, storage.Config{
    Driver: "oss", // 或 s3 / minio / cos / obs / local / memory
    // ...
})
```

| 组件 | 职责 |
|------|------|
| `storage.Register(name, Opener)` | 驱动自注册 |
| `storage.Open(ctx, Config)` | 按 `Driver` 查找并构造；可选套 `WithPrefix` |
| 各 `pkg/storage/{s3,oss,cos,obs}` | `init` 注册；封装厂商 SDK 或 S3 兼容客户端 |

### 2.2 驱动命名

| `driver` | 含义 | 推荐实现路径 |
|----------|------|----------------|
| `memory` | 进程内存 | 内置 |
| `local` | 本地目录 | 内置 |
| `s3` | AWS S3 或任意自定义 S3 Endpoint | AWS SDK v2 |
| `minio` | MinIO（S3 兼容，默认 path-style） | 复用 s3 实现 + 预设 |
| `oss` | 阿里云 OSS | 优先 S3 兼容 Endpoint；必要时原生 OSS SDK |
| `cos` | 腾讯云 COS | 优先 S3 兼容；必要时 COS SDK |
| `obs` | 华为云 OBS | 优先 S3 兼容；必要时 OBS SDK |

**两层实现策略：**

1. **L1（默认）— S3 兼容协议**：多数厂商提供 S3 兼容访问；共用 `storage/s3` 客户端，各厂商包只做 Endpoint/Region/PathStyle **预设与校验**。
2. **L2（按需）— 原生 SDK**：当需要厂商独有能力（回调、归档、图片处理、特殊鉴权）时，在对应子包内切换原生客户端，仍实现同一 `Store` / `Presigner`。

---

## 3. 选型对比

### 3.1 协议与 SDK

| 厂商 / 产品 | 协议选项 | Go SDK 候选 | 许可证（引入时复核） | 建议 |
|-------------|----------|-------------|----------------------|------|
| AWS S3 | S3 + SigV4 | `aws-sdk-go-v2` + `service/s3` | Apache-2.0 | **S3 实现基座** |
| MinIO | S3 兼容 | 同上（Endpoint + path-style） | — | 本地 / CI **首选** |
| 阿里云 OSS | OSS 原生 / S3 兼容 | L1: AWS SDK；L2: `alibabacloud-oss-go-sdk-v2` | Apache-2.0 | 国内默认 `driver=oss` |
| 腾讯云 COS | COS 原生 / S3 兼容 | L1: AWS SDK；L2: `cos-go-sdk-v5` | Apache-2.0 | `driver=cos` |
| 华为云 OBS | OBS 原生 / S3 兼容 | L1: AWS SDK；L2: `esdk-obs-go` | Apache-2.0 | `driver=obs` |

**统一约定：**

- 不引入 aws-sdk-go **v1**。
- L1 阶段 **禁止** AWS SDK 与 minio-go 双栈；S3 兼容一律 AWS SDK v2。
- L2 原生 SDK **仅**出现在对应子包，不泄漏到 `pkg/storage` 根包。

### 3.2 放置边界

| 放哪里 | 内容 |
|--------|------|
| `pkg/storage` | 契约、注册表、Memory/Local、键与错误 |
| `pkg/storage/{s3,oss,cos,obs}` | 可选驱动（可跨仓库复用） |
| `internal/platform` | 读 yaml/env、选 driver、blank-import、注入业务 |

驱动作为 **pkg 可选模块** 承载厂商协议细节；platform 只做装配。

---

## 4. 配置草案

```yaml
storage:
  driver: oss                 # memory|local|s3|minio|oss|cos|obs
  key_prefix: "prod/"         # 环境前缀；商户级再用 WithPrefix
  max_object_bytes: 33554432
  presign_put_ttl: 15m
  presign_get_ttl: 15m

  # 以下按 driver 选用；密钥禁止入库
  local:
    root: /data/objects

  # S3 兼容共用段（s3/minio/oss/cos/obs 的 L1 均可读）
  s3:
    endpoint: ""              # MinIO/兼容必填；AWS 可空
    region: ap-southeast-1
    bucket: admin-frame-prod
    force_path_style: false   # MinIO 常 true
    # ACCESS_KEY / SECRET_KEY → 环境变量
    # ADMIN_FRAME_STORAGE_S3_ACCESS_KEY
    # ADMIN_FRAME_STORAGE_S3_SECRET_KEY

  # 厂商覆盖（可选；空则回落 s3 段）
  oss:
    endpoint: oss-cn-hangzhou.aliyuncs.com
    region: cn-hangzhou
    bucket: admin-frame-prod
    native: false             # true=L2 原生 SDK（按需实现）
  cos:
    endpoint: cos.ap-guangzhou.myqcloud.com
    region: ap-guangzhou
    bucket: admin-frame-prod-1250000000
    native: false
  obs:
    endpoint: obs.cn-north-4.myhuaweicloud.com
    region: cn-north-4
    bucket: admin-frame-prod
    native: false

  lifecycle:
    apply_on_start: false       # true 时启动写桶策略（需对应驱动权限）
    rules:
      - id: abort-mpu
        prefix: ""
        abort_incomplete_multipart_days: 7
      - id: expire-tmp
        prefix: tmp/
        expire_days: 7

  # 命名附加桶（可选）；键不可为 default
  buckets:
    public:
      driver: s3
      key_prefix: "prod/public/"
      s3:
        region: ap-southeast-1
        bucket: admin-frame-public
```


`env=prod`：禁止 `memory`；`local` 仅单机明确交付。

---

## 5. 安全与多租户

1. 键：`{env}/{channel}/{merchant_id|user_id}/...`，强制 `ValidateKey`。
2. 桶默认私有；对外短 TTL PresignGet。
3. PresignPut 限制 Content-Type 白名单与大小。
4. A/B 附件/导出：`pkg/rbac` + 审计；C 端仅本人前缀。
5. 优先桶 SSE / KMS；应用层加密用 `pkg/crypto`。
6. Trace/日志：禁止 Secret 与完整 signed query（SEC-05）。

### 5.1 Multipart（M5）

- 契约：`pkg/storage.MultipartUploader`（`CreateMultipart` / `UploadPart` / `CompleteMultipart` / `AbortMultipart`）
- 实现：`pkg/storage/s3`；`PrefixStore` 转发并改写键；经 `storage.AsMultipart(store)` 探测
- 业务 Handler 尚未暴露 HTTP；大文件直传优先用 PresignPut

### 5.2 多桶（M5）

- 装配：`platform.OpenStorageSet` → `StorageSet{Default, named…}`；`Get("default"|name)`
- 配置：顶层 `storage` 为默认桶；`storage.buckets.<name>` 为附加桶（名不可为 `default`）
- 健康：`StorageSet.ReadyAll`；admin-api 另注册 `storage:<name>`
- 注入：业务按名取 Store（如 `stores.MustGet("public")`）；未指定时使用 `stores.Default`

### 5.3 生命周期（M5）

- 契约：`pkg/storage.LifecycleManager`（`PutLifecycle` / `GetLifecycle`）；`AsLifecycle` / `ApplyLifecycle`
- 实现：`pkg/storage/s3`（空 rules = 删除桶生命周期）
- 配置：`storage.lifecycle` / `buckets.*.lifecycle`；**仅** `apply_on_start: true` 时启动写入
- memory/local 不支持；校验拒绝对其开启 `apply_on_start`

---

## 6. 分期落地

| 阶段 | 交付 | 验收 |
|------|------|------|
| **M0** | 注册表 + `Open` + 内置 memory/local + 本文档 | ✅ |
| **M1** | `pkg/storage/s3`（含 `minio`）+ `storagetest` + `docker-compose.integration.yml` | ✅；联调：`go test -tags=minio ./pkg/storage/s3/` |
| **M2** | `oss` / `cos` / `obs` L1 + `ValidateCloudL1` | ✅ |
| **M3** | platform `OpenStorage` + yaml/env + 三端/worker 装配 + `/readyz` storage | ✅ |
| **M4** | 各厂商 L2 原生 SDK（`native=true`） | ✅ OSS / COS / OBS |
| **M5** | multipart、生命周期、多桶 | ✅ |

---

## 7. 测试策略

| 层级 | 内容 |
|------|------|
| 单测 | 注册表、配置校验、错误映射；Memory/Local 全量 |
| 契约测 | `storagetest`（可后续）：对任意 `Store` 跑同一套用例 |
| 集成 | CI 起 MinIO；`driver=minio` |
| 厂商冒烟 | 只读账号对 oss/cos/obs 兼容 Endpoint 的 List/Head |

---

## 8. 依赖与许可证（DEP-06）

| 直接依赖 | 用途 | 许可证 | 替代方案 |
|----------|------|--------|----------|
| `github.com/aws/aws-sdk-go-v2` + `service/s3` | L1 S3/MinIO/兼容基座 | Apache-2.0 | —（基座） |
| `github.com/aliyun/alibabacloud-oss-go-sdk-v2` | L2 阿里云 OSS 原生 | Apache-2.0 | L1 `native=false` + S3 兼容 Endpoint |
| `github.com/tencentyun/cos-go-sdk-v5` | L2 腾讯云 COS 原生 | Apache-2.0 | L1 S3 兼容 |
| `github.com/huaweicloud/huaweicloud-sdk-go-obs` | L2 华为云 OBS 原生 | Apache-2.0 | L1 S3 兼容 |

- 默认配置 `native: false`（L1）；进程 blank-import 驱动包后才会链接对应 SDK。
- 引入后须跑 `make vuln` / `make check-license`（CI）。

### 8.1 启用 L2

```yaml
storage:
  driver: oss          # 或 cos / obs
  oss:
    native: true
    endpoint: oss-cn-hangzhou.aliyuncs.com
    region: cn-hangzhou
    bucket: admin-frame-prod
    # access_key / secret_key → ADMIN_FRAME_STORAGE_S3_ACCESS_KEY / ADMIN_FRAME_STORAGE_S3_SECRET_KEY
```

校验：`storage.ValidateCloudL2`（bucket + AK/SK；oss/cos 要 region；obs 要 endpoint）。

---

## 9. 参考

- 代码：`pkg/storage`、`pkg/storage/{s3,oss,cos,obs}`
- 约束：SEC-01/05、DEP-01～06、REL 超时
- 协议：AWS SDK v2 S3 / Presign；各云「S3 兼容」官方说明
