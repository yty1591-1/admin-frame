# 对象存储：多云模块化驱动规划与选型

> **状态**：落地中（M0–M3b / M5-multipart 已完成；M4 与生命周期/多桶按需）  
> **关联**：[`pkg/storage`](../pkg/storage)、[`docs/pkg-roadmap.md`](./pkg-roadmap.md)、[`SECURITY_STANDARDS.md`](./SECURITY_STANDARDS.md)、[`DEPENDENCY_STANDARDS.md`](./DEPENDENCY_STANDARDS.md)、[`ARCHITECTURE.md`](./ARCHITECTURE.md) §十五  
> **最后更新**：2026-10-19  
> **目标读者**：后端 / 平台装配 / 安全 Review

---

## 1. 定位

对象存储是本仓库的**模块化基础设施能力**：

- **契约统一**：业务只依赖 `pkg/storage.Store` / `Presigner`，不感知厂商。  
- **驱动可插拔**：阿里云 OSS、腾讯云 COS、华为云 OBS、AWS S3、MinIO 等以**独立子包 + 注册表**接入（类比 `database/sql` 驱动模型）。  
- **装配可选**：`cmd` / `platform` **按需 blank-import** 所需驱动；未引用的驱动不进入进程。  
- **本地可测**：内置 `memory` / `local`；CI 可用 MinIO。

```
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

---

## 2. 目标与非目标

### 2.1 目标

1. 一套键规范、错误语义、前缀隔离（`Join` / `ValidateKey` / `WithPrefix`）覆盖所有后端。  
2. 配置用 `driver: s3|minio|oss|cos|obs|local|memory` 切换，业务代码零改动。  
3. 每种云作为**可选项**独立演进：可先走 S3 兼容协议，再按需补原生 SDK 能力。  
4. 大文件 **Presign 直传**；密钥走环境变量 / IAM / 云角色（SEC-01）；日志禁止完整签名串（SEC-05）。  
5. 新增 SDK 遵守 DEP-06（用途、许可证、替代方案）。

### 2.2 非目标

- 运行时热切换多云双写 / 自动 failover（另开 ADR）。  
- CDN、图片处理、内容审核（上传后异步流水线）。  
- 在领域层引用任何云 SDK。

---

## 3. 模块化模型

### 3.1 注册表（已实现方向）

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

### 3.2 驱动命名

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

## 4. 选型对比

### 4.1 协议与 SDK

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

### 4.2 放置边界

| 放哪里 | 内容 |
|--------|------|
| `pkg/storage` | 契约、注册表、Memory/Local、键与错误 |
| `pkg/storage/{s3,oss,cos,obs}` | 可选驱动（可跨仓库复用） |
| `internal/platform` | 读 yaml/env、选 driver、blank-import、注入业务 |

与早期「SDK 只放 platform」相比：驱动作为 **pkg 可选模块** 更符合「模块化项目能力 + 多可选项」；platform 只做装配，不实现厂商协议细节。

---

## 5. 配置草案

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
    # ADMIN_FRAME_STORAGE_ACCESS_KEY
    # ADMIN_FRAME_STORAGE_SECRET_KEY

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
```


`env=prod`：禁止 `memory`；`local` 仅单机明确交付。

---

## 6. 安全与多租户

1. 键：`{env}/{channel}/{merchant_id|user_id}/...`，强制 `ValidateKey`。  
2. 桶默认私有；对外短 TTL PresignGet。  
3. PresignPut 限制 Content-Type 白名单与大小。  
4. A/B 附件/导出：`pkg/rbac` + 审计；C 端仅本人前缀。  
5. 优先桶 SSE / KMS；应用层加密用 `pkg/crypto`。  
6. Trace/日志：禁止 Secret 与完整 signed query（SEC-05）。

### 6.1 A 端 files API（M3b）

业务键约束在 `files/{category}/YYYY/MM/{id}_{filename}`；权限点 `file:read` / `file:write`（`admin` 超管、`ops` 已授予）。

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| POST | `/admin/v1/files` | `file:write` | multipart 服务端中转上传（`file` + `category`） |
| POST | `/admin/v1/files/uploads/presign` | `file:write` | 云驱动 PresignPut；memory/local 返回 BadRequest |
| GET | `/admin/v1/files/downloads/presign?key=` | `file:read` | PresignGet |
| GET | `/admin/v1/files/object?key=` | `file:read` | 服务端流式下载 |

代码：`internal/files/app`、`internal/files/transport/admin`；装配：`cmd/admin-api`。

### 6.2 Multipart（M5 起步）

- 契约：`pkg/storage.MultipartUploader`（`CreateMultipartUpload` / `UploadPart` / `Complete` / `Abort`）
- 实现：`pkg/storage/s3`；经 `storage.AsMultipart(store)` 探测
- 业务 Handler 尚未暴露 HTTP；大文件直传优先用 PresignPut

---

## 7. 分期落地

| 阶段 | 交付 | 验收 |
|------|------|------|
| **M0** | 注册表 + `Open` + 内置 memory/local + 本文档 | ✅ |
| **M1** | `pkg/storage/s3`（含 `minio`）+ `storagetest` + `docker-compose.storage.yml` | ✅；联调：`go test -tags=minio ./pkg/storage/s3/` |
| **M2** | `oss` / `cos` / `obs` L1 + `ValidateCloudL1` | ✅ |
| **M3** | platform `OpenStorage` + yaml/env + 三端/worker 装配 + `/readyz` storage | ✅ |
| **M3b** | A 端 `internal/files`：服务端上传 + Presign 上传/下载（`file:read/write`） | ✅ |
| **M4** | （按需）各厂商 L2 原生 SDK | 待做 |
| **M5** | multipart、生命周期、多桶 | 进行中：`MultipartUploader` + S3 实现 ✅；生命周期/多桶待做 |

---

## 8. 测试策略

| 层级 | 内容 |
|------|------|
| 单测 | 注册表、配置校验、错误映射；Memory/Local 全量 |
| 契约测 | `storagetest`（可后续）：对任意 `Store` 跑同一套用例 |
| 集成 | CI 起 MinIO；`driver=minio` |
| 厂商冒烟 | 只读账号对 oss/cos/obs 兼容 Endpoint 的 List/Head |

---

## 9. 依赖与许可证

- 每引入一个原生 SDK：独立 MR，填写 DEP-06，跑 `make vuln` / `make check-license`。  
- L1 仅 AWS SDK v2 时，oss/cos/obs 子包可只依赖 `storage/s3`，**不强制**三家原生 SDK 同时进 `go.mod`。  
- L2 落地某厂商时，再将其 SDK 加入直接依赖并更新本表。

---

## 10. 开放问题（Owner 可关）

| 问题 | 建议默认 |
|------|----------|
| 国内是否允许只用 S3 兼容、不上原生？ | M2 先 L1；遇阻再 L2 |
| 是否支持同进程多 Store（公有静态桶 + 私有附件桶）？ | 支持；platform 按名注入多个 `Open` 结果 |
| 驱动包是否拆独立 Go module？ | 近期单 module；依赖膨胀再拆 |

---

## 11. 参考

- 代码：`pkg/storage`、`pkg/storage/s3|oss|cos|obs`  
- 路线图：`docs/pkg-roadmap.md`  
- 约束：SEC-01/05、DEP-01～06、REL 超时  
- AWS SDK v2 S3 / Presign；各云「S3 兼容」官方说明  
