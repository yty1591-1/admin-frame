# ADR-0016：配置读取收敛为 config-api 拉取模型（类 Apollo）

- 状态：Accepted（已接受，2026-11-16）
- 日期：2026-11-16
- 关联：[ADR-0013](0013-config-center-in-process-snapshot.md)（被本 ADR 取代决策 4 / 10）、[ADR-0014](0014-secret-management-envelope-encryption.md)、[ADR-0015](0015-config-hot-reload-whitelist.md)、[`../ARCHITECTURE.md`](../ARCHITECTURE.md) §4.4 / §5.6 / §17、[`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md)

## 背景

ADR-0013 决策 4 / 10 规定：业务进程以只读账号直连 MySQL 拉取配置，并用 Ed25519 签名快照作为冷启动前置。M3 收口后进一步强制 `RequireLoad`，导致：

- 首次启动必须离线生成快照与公钥，形成「先有鸡还是先有蛋」；
- 每个业务进程持有配置库读连接，扩容成本与权限面扩大；
- 与行业主流（Apollo / Nacos：独立配置服务 + SDK 启动拉取 + 本地缓存灾备 + 长轮询）不一致。

ADR-0013 后续行动 5 已预留「M2 后复评普通配置读是否收敛到 config-api」。本 ADR 完成该复评并改定读路径。

## 决策

1. **config-api 成为普通配置读与密钥解析的唯一服务端**：新增 `GET /internal/v1/configs/{env}/{namespace}`（拉取，支持 `release_key` 304）与 `GET /internal/v1/configs/notifications`（长轮询，最长 60s）；保留密钥 resolve。管理面仍挂 admin-api。
2. **业务进程不再直连配置库**：`pkg/configclient` 通过 HTTP 访问 config-api；启动拉取成功写本地缓存目录，失败读缓存，都没有则 fail-fast。
3. **彻底移除快照签名**：信任靠 TLS + 服务身份（`CONFIG_API_MTLS` 三档）。本地缓存文件权限 0600，接受「可被本机特权进程篡改」的风险。
4. **播种基线迁到 config-api**：按 key spec 幂等播种 Release 与 auto_generate 密钥；admin-api 仅保留管理面，不再在启动期播种。
5. **读取授权**：通过身份校验的服务可读全部非敏感 namespace；不做按服务白名单（后续可 ADR 收紧）。
6. **热更机制不变**：ADR-0015 白名单与 `hotconfig.View` 原子替换保留；驱动由 5s 直连轮询改为长轮询唤醒后再拉取。
7. **引导变量**：`CONFIG_API_URL`、`CONFIG_SERVICE_IDENTITY`、`CONFIGCENTER_ENV`、可选 `CONFIGCENTER_NAMESPACES` / `CONFIGCENTER_CACHE_DIR`、TLS 变量，加 `ADMIN_FRAME_DB_*` / `ADMIN_FRAME_REDIS_*`。移除 `CONFIGCENTER_SNAPSHOT_*`、`CONFIGCENTER_ENABLED_NAMESPACES`、`CONFIGCENTER_ALLOW_MISSING_CONFIG`。

## 备选方案

| 方案 | 不选理由 |
|------|----------|
| 保留签名为可选 | 运维仍需生成密钥对；与「彻底简化启动」目标冲突 |
| 非 prod 无缓存可用代码默认值 | 掩盖部署错误；与 fail-fast 决策冲突 |
| 按服务授权 namespace | 首期复杂度高；身份校验已足够边界 |

## 后果

**正面**：首次启动顺序为 compose → migrate → config-api → 业务进程；业务进程零配置库读权限；读路径与密钥路径统一。

**负面**：config-api 成为启动强依赖，需多副本；本地缓存仅在曾成功拉取的实例上有效。

**守卫**：`check-configcenter.sh` 断言拉取 / 通知端点、OpenAPI、四进程 `configboot.Load`、无签名残留。

## 取代关系

- 取代 ADR-0013 **决策 4**（进程内直连 MySQL + 签名快照冷启动）与 **决策 10**（Ed25519 签名快照兜底）。
- ADR-0013 其余决策（自研、MySQL 事实源、不可变 Release、灰度契约、权限、密钥信封）仍然有效。
