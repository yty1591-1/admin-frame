# admin-frame-frontend Chart

前端静态站点 Chart，与后端 `admin-frame` Chart **独立发布与回滚**（INF-REL-03）。
一个 Release 可同时管理 A 端 `admin` 与 B 端 `biz` 两个工作负载，互不影响。

## 依赖

- Kubernetes ≥ 1.27
- 已构建并推送的镜像（`deploy/frontend/Dockerfile`，非 root Nginx，监听 8080）
- 镜像拉取凭据（私有仓库时通过 `imagePullSecrets` 注入）

## 安装

Release 名建议使用 `admin-frame-frontend`，这样生成的资源名与 Chart 名一致：

```bash
helm upgrade --install admin-frame-frontend deploy/helm/admin-frame-frontend \
  --namespace admin-frame --create-namespace \
  -f deploy/helm/admin-frame-frontend/values-prod.yaml \
  --set workloads.admin.image.digest=sha256:<64-hex> \
  --set workloads.biz.image.digest=sha256:<64-hex> \
  --atomic --wait --timeout 5m
```

**禁止** `--set image.tag=latest`；未设置 `digest` 时回退 `tag`，`tag` 为空时回退 `Chart.AppVersion`。

## 关键参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `workloads.admin.enabled` | `true` | 是否部署 A 端 |
| `workloads.biz.enabled` | `true` | 是否部署 B 端 |
| `workloads.admin.image.repository` | `admin-web` | A 端镜像仓库路径 |
| `workloads.biz.image.repository` | `biz-web` | B 端镜像仓库路径 |
| `workloads.admin.image.digest` | `""` | 生产必填，优先于 `tag` |
| `workloads.biz.image.digest` | `""` | 同上 |
| `workloads.admin.replicas` | `2` | A 端副本数 |
| `workloads.biz.replicas` | `2` | B 端副本数 |
| `workloads.<name>.runtime` | 见 values | 渲染到 `config.js` 的非敏感运行期配置 |
| `workloads.<name>.ingress.*` | 关闭 | 主机名、TLS、注解 |
| `workloads.<name>.pdb.*` | 关闭 | 驱逐保护 |

`runtime` 会整体序列化为 `window.__APP_CONFIG__`；修改后 `checksum/config` 变化会自动滚动 Pod。

## 发布后自检

```bash
kubectl -n admin-frame rollout status deploy/admin-frame-frontend-admin --timeout=120s
curl -sSI https://admin.example.com/index.html | grep -i cache-control
curl -sSI https://admin.example.com/config.js  | grep -i cache-control
```

完整检查项见 Chart 安装后的 NOTES 与 [`docs/runbooks/frontend-rollback.md`](../../../docs/runbooks/frontend-rollback.md)。

## 回滚

```bash
helm history admin-frame-frontend -n admin-frame
helm rollback admin-frame-frontend <REVISION> -n admin-frame --wait --timeout 5m
```

前端回滚只替换静态产物与配置；**不需要**数据库迁移回滚。
