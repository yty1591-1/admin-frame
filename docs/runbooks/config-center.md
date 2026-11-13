# 配置中心告警处置（Config Center）

> 适用：`config.client_stale`、`config.publish_failure`、`config.fetch_error`、
> `config.pending_approval`、`config.db_latency`、`config.rollback`、
> `config.snapshot_corrupt`、`config.cold_start_degraded`。
> 关联规范：`docs/CONFIG_STANDARDS.md`（CFG-01～12）、`docs/config-center-plan.md` §5.6。
> 原则：**配置中心不可用时进程继续使用最后一份已验签快照**，不得为了「看起来正常」而关闭验签。

## 症状与告警

| 告警 | 典型症状 | 首要判断 |
|------|----------|----------|
| `config.client_stale` | 快照年龄持续增长，管理面改配置不生效 | 刷新失败还是中心不可用 |
| `config.publish_failure` | 发布返回 5xx / 409，指针未前移 | 数据库、签名服务、CAS 冲突 |
| `config.fetch_error` | 客户端拉取失败率上升，日志 `refresh failed` | DB / 网络 / 快照过大 |
| `config.snapshot_corrupt` | `snapshot verify` 失败，快照被拒 | 文件被篡改、公钥 key_id 不匹配 |
| `config.cold_start_degraded` | 进程以代码默认值启动 | 启动期 DB 或快照文件不可用 |
| `config.pending_approval` | 发布单长期停在待审批 | 审批人缺席或流程阻塞 |
| `config.db_latency` | 管理面 P99 升高 | 慢查询、连接池、锁竞争 |
| `config.rollback` | 出现回滚审计事件 | 回滚原因与影响面 |

## 影响与范围

- 客户端只读本地快照，中心故障时**读路径不受影响**，影响面是「配置变更不可见」。
- 管理面故障影响发布、回滚、审批，不影响已生效配置。
- 快照验签失败属于**可信性事件**：按安全事件处理，禁止绕过验签直接装载。
- 冷启动降级意味着进程使用代码默认值，可能缺少灰度 / 限流等运营参数，需评估是否摘流。

## 排查步骤

1. **确认范围**：`max by (namespace) (config_client_snapshot_age_seconds)`，判断是单 namespace 还是全中心。
2. **看客户端日志**：检索 `configcenter refresh`，区分 `fetch`、`verify`、`apply` 三阶段错误。
3. **看管理面**：`rate(config_admin_request_total{result="fail"}[5m])` 与
   `histogram_quantile(0.99, sum by (le) (rate(config_admin_request_duration_seconds_bucket[5m])))`。
4. **看数据库**：配置表连接、慢查询、锁等待；确认 `config_snapshot` / `config_release_pointer` 可读。
5. **核对快照**：`config_client_snapshot_corrupt_total` 增长时，比对本地快照 `key_id` 与当前公钥集，
   确认文件哈希与签名；不要手工改写快照内容。
6. **核对发布单**：`config_release_pending_approval` 与发布单状态，确认是否卡在审批 / 指针 CAS。
7. **核对回滚**：检索审计事件 `config.release.rollback`，确认操作人、目标 Release 与原因。

## 缓解与恢复

### 客户端快照过期 / 拉取失败

- 保留最后一份可用快照，**不要删除本地快照文件**；确认 DB 与网络恢复后等待下一轮刷新（≤ 1 分钟）。
- 若仅个别进程异常，重启该进程可触发启动期重新拉取；重启前确认快照文件存在且验签通过。

### 快照损坏 / 验签失败

- 立即停止发布，保留现场（快照文件、公钥配置、审计记录）。
- 核对 `CONFIG_SNAPSHOT_PUBLIC_KEYS` 的 `key_id` 是否包含签名所用密钥；公钥轮换必须先发布新公钥。
- 确认非预期篡改后，从上一版可信快照恢复并重新发布；按安全事件上报流程留痕。

### 无文件启动演练 / 应急启动

1. 确认 DB 中 `config_release_pointer` 指向的 Release 存在且 `status=published`。
2. 确认签名私钥可用、公钥已下发到目标进程。
3. 设置 `CONFIGCENTER_SNAPSHOT_FILE`、`CONFIGCENTER_SNAPSHOT_PUBLIC_KEYS`，并显式开启
   `CONFIGCENTER_ALLOW_MISSING_CONFIG=1`（只有在快照非空时才会生效）。
4. 移除（或改名）本地 `configs/` 下的非密钥运行时文件，观察启动日志：
   - 成功：日志出现快照加载与验签通过，配置读取不再依赖文件。
   - 失败：回退到恢复文件并保留现场，禁止带病启动。
5. 启动后核对关键配置与预期 Release 一致，再恢复流量。

### 回滚

- 使用管理面「回滚」到最近一个 `published` Release，确认指针前移与客户端版本号变化。
- 回滚同样走审批与审计；回滚后观察 `config_client_version` 是否在刷新周期内更新。
- 数据库迁移类配置不可回滚，须按发布单记录人工处置。

### 双读差异

- 双读在四个进程**启动期执行一次**，之后**每 30s 跟随快照刷新采样**：把配置文件展平后，与**每个启用 namespace** 的签名快照逐键比对；只记录 key 与哈希，不记录取值。
- 差异指标：`config_dual_read_mismatch{namespace,key}`（1 差异 / 0 一致）、`config_dual_read_degraded{namespace}`（1 表示中心不可用、已回落基线）；日志检索 `config dual-read`。
- 出现差异时，以配置中心值为准，记录差异 key 与文件来源，禁止直接修改快照。
- 差异未归零前不得删除对应 `configs` 文件，也不得关闭双读；`missing_in_center > 0` 表示迁移未覆盖，同样阻断切换。
- 未登记 spec 的业务域键暂不参与比对，需先补齐键声明再纳入观察。
- 采样日志已去重：`config dual-read mismatch` 只在差异集合变化时输出，`config dual-read summary` 只在计数或版本变化时输出；指标每 30s 刷新一次，可直接用于巡检。

### 7 天双读 soak（AC-CC-11 取证）

前置：

1. staging 开启 `configCenter.enabled=true`，快照 Secret 与验签公钥已下发。
2. `CONFIGCENTER_ENABLED_NAMESPACES` 覆盖本轮待迁移的全部 namespace（不要只开 `platform` 就宣称全覆盖）。
3. 四个进程为含「双读周期采样」的镜像；启动日志应出现 `config dual-read periodic enabled`。

每日巡检（PromQL）：

- 差异键数量：`sum(config_dual_read_mismatch) by (namespace)` 必须恒为 0。
- 降级：`sum(config_dual_read_degraded) by (namespace)` 必须恒为 0；出现 1 说明中心不可用，本轮 soak 暂停并记录原因。
- 采样未中断：日志中当天仍有 `config dual-read summary`（计数变化时输出）或指标序列持续存在。

达标口径：

- 连续 7 天每日指标为 0，且期间至少完成一轮「发布 → 生效 → 回滚」，差异仍为 0。
- 出现 `missing_in_center > 0` 需补齐键声明或迁移后**重新计时**，不可累计。
- 任一差异、降级或采样中断即视为本轮失败，修复后从零重新计 7 天。

退出条件：双读归零并满 7 天后，才可进入非密钥 `configs` 基线收缩；删除文件后需再做一次无文件启动演练。

### namespace 接管开关

- 启动期快照文件：`CONFIGCENTER_SNAPSHOT_FILES=/etc/configcenter/platform.json,/etc/configcenter/identity.json`（逗号分隔，每份独立签名；未设置时退回 `CONFIGCENTER_SNAPSHOT_FILE` 单文件）。
- 接管开关：`CONFIGCENTER_ENABLED_NAMESPACES=platform,identity`；`all` / `*` 表示加载到的全部 namespace；空值等价于 `platform`（保持旧行为）。
- 只有启用 namespace 的取值会覆盖配置；未启用的快照即使挂载也不生效，便于按 namespace 灰度。
- 开关按进程生效，变更需重启；重复 namespace、验签失败、缺少公钥均启动失败（降级为文件配置并告警）。
- 启用但未提供快照的 namespace 不会覆盖取值，进程继续使用文件 / 默认值；切换前请确认双读差异为零。
- MySQL 模式下 admin-api / biz-api / c-api / worker 都会启动刷新客户端，暴露快照版本、年龄与 stale 指标；内存模式不启动。刷新值目前不回写运行期配置，热更需先立白名单 ADR。
- Helm 使用 `configCenter.*`：`enabled`、`secretName`（存放快照文件的 Secret）、`mountPath`、`snapshotFiles`（逗号分隔文件名）、`enabledNamespaces`、`allowMissingConfig`、`publicKeys.secretName/key`；渲染后自动生成上述环境变量并挂载只读快照目录。

## 升级路径

- P1（`client_stale` / `publish_failure` / `rollback`）：15 分钟内未恢复，升级至平台负责人与安全负责人；
  涉及验签、密钥、越权发布时同步安全事件流程。
- P2：30 分钟内未定位，升级至配置中心 owner；涉及多 namespace 或全中心时按 P1 处理。
- 任何人工修改快照、公钥或指针的操作必须双人复核并留审计。

## 相关链接

- 规划与验收：`docs/config-center-plan.md`
- 配置规范：`docs/CONFIG_STANDARDS.md`
- 密钥与签名：`docs/adr/0014-secret-management-envelope-encryption.md`
- 告警目录：`docs/runbooks/ALERTS.md`
- 指标实现：`internal/configcenter/observability/metrics.go`
