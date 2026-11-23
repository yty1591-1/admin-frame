# 配置中心告警处置（Config Center）

> 适用：`config.client_stale`、`config.publish_failure`、`config.fetch_error`、
> `config.pending_approval`、`config.db_latency`、`config.rollback`、
> `config.snapshot_corrupt`、`config.cold_start_degraded`。
> 关联规范：`docs/CONFIG_STANDARDS.md`（CFG-01～12）、[ADR-0016](../adr/0016-config-service-pull-model.md)、[ADR-0017](../adr/0017-config-center-install-state.md)。
> 原则：业务进程经 **config-api 拉取**；远端失败读本地缓存；二者皆无则启动失败。
> 首次安装（prod-like）：`GET /internal/v1/install/status`；未 `ready` 时业务 `/readyz` 503；用 setup token 走 Admin `/setup/install` 手动完成（`scripts/prod` 仅最小启动，不自动安装）。

## 症状与告警

| 告警 | 典型症状 | 首要判断 |
|------|----------|----------|
| `config.client_stale` | 快照年龄持续增长，管理面改配置不生效 | 长轮询失败还是 config-api 不可用 |
| `config.publish_failure` | 发布返回 5xx / 409，指针未前移 | 数据库、CAS 冲突 |
| `config.fetch_error` | 客户端拉取失败率上升 | config-api / 网络 / 身份 |
| `config.snapshot_corrupt` | 本地缓存完整性校验失败 | 缓存文件被篡改 |
| `config.cold_start_degraded` | 冷启动无可用缓存（历史指标；现多为 fail-fast） | 首次部署未先起 config-api |
| `config.pending_approval` | 发布单长期停在待审批 | 审批人缺席或流程阻塞 |
| `config.db_latency` | 管理面 P99 升高 | 慢查询、连接池、锁竞争 |
| `config.rollback` | 出现回滚审计事件 | 回滚原因与影响面 |

## 影响与范围

- 运行期：config-api 短暂不可用时，进程继续使用内存中上一份配置；本地缓存仅服务重启灾备。
- 管理面故障影响发布、回滚、审批，不影响已装载配置。
- 缓存完整性失败属于可信性事件：拒绝装载该文件，依赖远端或 fail-fast。
- 首次部署无缓存时，config-api 是启动强依赖，须多副本。

## 排查步骤

1. **确认范围**：`max by (namespace) (config_client_snapshot_age_seconds)`。
2. **看客户端日志**：检索 `configboot` / `configcenter refresh`，区分拉取、缓存、长轮询。
3. **看 config-api**：`/readyz`、NetworkPolicy、服务身份头、mTLS（`CONFIG_API_MTLS`）。
4. **看管理面**：`rate(config_admin_request_total{result="fail"}[5m])` 与延迟直方图。
5. **看数据库**：仅 config-api / admin-api 读写配置表；业务库账号可收回配置表读权限。
6. **核对发布单**：`config_release_pending_approval` 与指针 CAS。
7. **核对回滚**：审计事件 `config.release.rollback`。

## 缓解与恢复

### 客户端拉取失败 / 过期

- 保留本地缓存目录（`CONFIGCENTER_CACHE_DIR`，默认 `var/configcenter-cache`），权限 0600。
- 确认 `CONFIG_API_URL`、`CONFIG_SERVICE_IDENTITY`、TLS 后等待长轮询唤醒（约 1s）或重启拉取。

### 缓存损坏

- 删除损坏的 namespace 缓存文件，依赖 config-api 重新拉取；首次无远端则进程退出。
- 不要手工改写缓存 JSON。

### 首次部署 / 安装向导

1. 先起 `config-api`：`CONFIGCENTER_ENV` 与 `CONFIG_INSTALL_SETUP_TOKEN` 都必填。启动后只 `EnsureState(pending)`，**不再自动播种基线**——所有环境统一由安装向导播种（CFG-09 / ADR-0018）。
2. 起 `admin-api`：安装态未 `ready` 时它以占位 JWT 起，只暴露安装向导代理；浏览器打开 `/setup/install`，用 setup token 依次 `begin → apply → complete`（本地 `make local-start` / `make local-install` 会自动驱动）。
3. 业务进程设置：
   - `CONFIG_API_URL`
   - `CONFIG_SERVICE_IDENTITY`
   - `CONFIGCENTER_ENV`（必须在 Validate 前注入进程身份证；禁止 Load 后再补写）
   - 可选 `CONFIGCENTER_NAMESPACES` / `CONFIGCENTER_CACHE_DIR`
4. **config-api（staging/prod）额外强制（CC-R-04）**：
   - `CONFIG_API_MTLS=require`
   - `CONFIG_API_TLS_CERT` / `CONFIG_API_TLS_KEY` / `CONFIG_API_TLS_CLIENT_CA`
   - `CONFIG_API_NAMESPACE_ALLOWLIST`（例：`admin-api:platform,identity;biz-api:platform,identity;*:platform`）
5. 确认拉取成功后再挂流量。
6. staging/prod 禁止业务密钥裸 env（短信 AK、通知盐/回调密钥、OAuth client_secret_env 等）；须走 secretclient（CC-R-03/05）。

### 缺键阻塞启动（setup-only）与 repair

- 症状：`/readyz` 返回 503，`checks[config_install].error` 形如 `config_incomplete: missing required keys: platform/auth.jwt.issuer; run admin install wizard`；日志首行固定 `SETUP-ONLY MODE: missing required keys ...`；admin-api 仍可打开安装向导，biz/c/worker 保持 crash-loop（启动日志 `config incomplete: missing required keys: ...`）。
- 降级态的暴露面：只有 `/healthz`、`/readyz`、`/version` 与 `/admin/v1/install/*`；`/metrics` 与全部业务路由 404，别拿降级态的 404 当成网关故障。
- 退出方式：安装完成后 admin-api **自动退出（退出码 0）**，由编排重启为正常进程；无编排环境把它当成长驻服务时，设 `ADMIN_FRAME_SETUP_ONLY_AUTO_EXIT=false`，修复后手工重启。`/readyz` 恢复 200 才可挂流量。
- 定位：`GET /admin/v1/install/status` 看 `gaps` 与 `blocking_count`；`GET /admin/v1/install/schema` 看这些键的类型/默认值/校验规则。
- 修复：`POST /admin/v1/install/begin` 带 `reason:"repair"`（服务端把阻塞键冻结成 `repair_keys` 白名单）→ 向导只渲染阻塞键 → `apply` → 响应 `remaining_gaps` 为空 → `complete`。
- `complete` 时仍有阻塞键返回 `40000`，状态保持 `installing`（租约 15 分钟）：可继续 `apply`，不必重新 `begin`。
- 白名单外的键一律 400：repair 不是日常配置渠道，非阻塞变更仍走 `submit → approve → publish`。
- 「键存在但取值非法」不降级（避免静默带病运行）：按 `config: invalid config: config validation failed: ...` 定位键，走管理面发布修正。
- 注意覆盖口径：`gaps` 只统计 key spec 的 `Required` 键。`auth.jwt.secret` 缺失、KEK/mTLS 证书错误这类「非快照」故障仍是 crash-loop，不会进降级态——按 §密钥与证书排障，别在向导里找答案。

### 回滚

- 管理面回滚到历史 Release 须显式 `break_glass=true` + `break_glass_reason`（CC-R-08；绕过双人审批）。
- 审计事件 `config.release.rollback` 含破玻璃标记；**24h 内**由第二人补审并留痕。
- 观察长轮询 1s 量级唤醒与 `config_hot_view_version`。

### Helm 引导

- `configCenter.enabled`、`apiURL`（空则推导 Release 内 config Service）、`env`、`namespaces`、`cacheDir`、`cacheVolume`（emptyDir）。
- 业务 workload 注入 `CONFIG_API_*` / `CONFIGCENTER_*`；`config` workload 自身不注入拉取变量。

### 热更生效验证

逐键验证「改了真的生效」，不要只看 `config_hot_key_applied_total` 递增。

| 键 | 验证动作 | 期望 |
|----|----------|------|
| `log.level` | 改为 `debug` 并发布 | 一个刷新周期内出现 debug 日志；`config_hot_view_version` +1 |
| `rate_limit.rps` / `burst` | 把 `burst` 调到明显偏小（如 2） | 持续压测在第 3 个请求开始返回 429；调回后恢复 |
| `async.outbox.poll_interval` | 调到 `5s` | worker 投递延迟升到 5s 量级 |

通用断言：

- **startup 键**：改 `server.addr` 并发布 → 不生效（需重启），`config_hot_ignored_total` 递增。
- **非法值**：`config_hot_reload_total{result="rejected"}` 递增，旧视图保留。

## 升级

已移除：`CONFIGCENTER_SNAPSHOT_FILE(S)`、`CONFIGCENTER_SNAPSHOT_PUBLIC_KEYS`、
`CONFIGCENTER_SNAPSHOT_SIGNING_KEY*`、`CONFIGCENTER_ENABLED_NAMESPACES`、
`CONFIGCENTER_ALLOW_MISSING_CONFIG`。请改用上述拉取引导变量，并先起 config-api。

## 相关链接

- [ALERTS.md](./ALERTS.md)
- [secret-management.md](./secret-management.md)
- [CONFIG_STANDARDS.md](../CONFIG_STANDARDS.md)
- [ADR-0016](../adr/0016-config-service-pull-model.md)
- [config-center-plan.md](../config-center-plan.md)
