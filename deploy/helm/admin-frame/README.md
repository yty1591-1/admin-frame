# admin-frame Helm Chart（ENT-05）

部署 `admin-api` / `biz-api` / `c-api` / `worker` 与迁移 Job，包含探针、资源、HPA、PDB、
NetworkPolicy、ServiceMonitor 与安全上下文。

## 1. 结构

```text
deploy/helm/admin-frame/
├── Chart.yaml
├── values.yaml            # 默认值（开发基线）
├── values-dev.yaml        # 内存依赖，本地预览
├── values-kind.yaml       # kind 验证基线（可重复跑部署/冒烟/回滚）
├── values-staging.yaml    # 真实依赖，2 副本
├── values-prod.yaml       # 3 副本 + HPA + PDB + 网络策略
└── templates/
    ├── _helpers.tpl       # 名称/标签/镜像/配置渲染
    ├── _workload.tpl      # Deployment 或 StatefulSet 共享模板
    ├── deployment-{admin,biz,c,worker}.yaml
    ├── configmap.yaml     # 每个 workload 一份应用配置
    ├── service.yaml / service-headless.yaml
    ├── ingress.yaml / hpa.yaml / pdb.yaml
    ├── networkpolicy.yaml / servicemonitor.yaml
    ├── serviceaccount.yaml     # pre-hook（weight -20）
    ├── migrate-config.yaml     # pre-hook（weight -10），迁移专用配置
    ├── migrate-job.yaml        # pre-install/pre-upgrade Hook（weight 0）
    └── NOTES.txt
```

**Hook 顺序是发布正确性的前提**：`ServiceAccount(-20) → migrate-config(-10) →
migrate Job(0) → 其余资源 → Deployment`。迁移必须先于 API 完成，否则新代码会在
旧 schema 上运行；迁移失败时整次发布中断、API 不被替换。
`make check-deploy` 用断言锁住这个顺序，不要改回 `post-*`。

## 2. 安装

```bash
# 渲染检查（不连集群）
helm lint deploy/helm/admin-frame
helm template admin-frame deploy/helm/admin-frame -f deploy/helm/admin-frame/values-staging.yaml > /tmp/render.yaml

# 预发
helm upgrade --install admin-frame deploy/helm/admin-frame \
  -n admin-frame --create-namespace \
  -f deploy/helm/admin-frame/values-staging.yaml \
  --set image.tag=sha-abcdef1 \
  --atomic --wait --timeout 10m

# 生产（镜像必须固定 digest，见 values-prod.yaml）
helm upgrade --install admin-frame deploy/helm/admin-frame \
  -n admin-frame --create-namespace \
  -f deploy/helm/admin-frame/values-prod.yaml \
  --atomic --wait --timeout 15m
```

`--atomic --wait` 是必需项：迁移 Job 失败会回滚到上一 revision（AC-ENT-05-2）。

## 3. 关键约定

| 项 | 说明 |
|---|---|
| 镜像 | `<registry>/<namespace>/<admin-api|biz-api|c-api|worker|migrate>:<tag>`；`tag` 空则回退 `Chart.AppVersion`；禁止 `latest`；`digest` 优先 |
| 配置 | 每个 workload 一份 ConfigMap（`config.yaml`），挂载到 `/etc/admin-frame`；`service`/`channel`/`addr`/`id` 由 Chart 注入 |
| 密钥 | 全部通过 `env` 的 `secretKeyRef`（见 `values-prod.yaml`）；Chart 不渲染任何明文密钥 |
| 探针 | API：`startup/live=/healthz`、`readiness=/readyz`；worker 不监听 HTTP，`probes.enabled=false`，用进程存活 + 积压告警兜底 |
| 多副本 ID | `statefulset.enabled=true` 时 `id.node_id_source=statefulset`（HOSTNAME ordinal）；Deployment 模式使用 `nodeID` 固定值，仅适合单副本 |
| 迁移 | `migrate.enabled=true` 时由 Hook Job 执行 `migrate -action up`，镜像内含 `/app/migrations` |
| 监控 | `serviceMonitor.enabled=true` 需要 Prometheus Operator CRD；规则见 `deploy/observability/prometheus` |
| 网络 | `networkPolicy.enabled=true` 默认拒绝，放行 Ingress→API:8080 与 DNS/DB/Redis/OTel 出站 |

## 4. 需要预先创建的 Secret

| Secret | Key | 用途 |
|---|---|---|
| `admin-frame-db` | `dsn` | `ADMIN_FRAME_DB_DSN` |
| `admin-frame-redis` | `password` | `ADMIN_FRAME_REDIS_PASSWORD` |
| `admin-frame-jwt` | `public.pem` | RS256 公钥，挂载到 `/etc/admin-frame/jwt` |
| `admin-frame-storage` | `access_key` / `secret_key` | 对象存储凭证 |
| `admin-frame-identity` | `mfa_secret_key` | 启用身份域时必填（32 字节） |
| `admin-frame-notification` | `callback_secret` / `blacklist_salt` | c-api 回调验签（≥32 字节）与 worker 黑名单 HMAC 盐；两者缺失会在装配期直接拒绝启动 |

密钥由 External Secrets / Vault / 云 Secrets Manager 同步，禁止 `--set` 明文传入。

## 5. 校验与门禁

```bash
bash scripts/ci/check-deploy.sh   # 结构 + helm template（可用时）+ 明文密钥/digest 断言
```

CI 使用 `azure/setup-helm` 安装 Helm 后执行 `helm template` 与 `kubeconform`；
生产发布必须使用 `--atomic --wait`。

## 6. 回滚

```bash
helm history admin-frame -n admin-frame
helm rollback admin-frame <revision> -n admin-frame --wait
```

已执行迁移的回滚需配套 down 迁移与数据库备份，见
[`docs/runbooks/release-rollback.md`](../../../docs/runbooks/release-rollback.md) 与
[`docs/runbooks/backup-restore.md`](../../../docs/runbooks/backup-restore.md)。
