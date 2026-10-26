# 前端发布产物（S4-2）

把 `apps/admin-web`、`apps/biz-web` 的静态产物打成非 root Nginx 镜像，并提供 Helm 工作负载。
对应规划项：INF-REL-02（静态服务与缓存头）、INF-REL-03（镜像与部署模板）、INF-REL-05（回滚）。

| 文件 | 说明 |
|---|---|
| `Dockerfile` | 多阶段构建；`nginxinc/nginx-unprivileged` 运行；以 UID/GID 101 监听 8080 |
| `nginx.conf` | 站点配置：缓存头、SPA 回退、`/healthz`、gzip |
| `security-headers.conf` | 每个 location 显式引入的安全响应头 |
| `../../deploy/helm/admin-frame-frontend` | 前后端独立发布的 Helm Chart（Deployment/Service/Ingress/PDB） |

## 构建与推送

构建上下文为**仓库根目录**（需要同时访问前端源码与本目录的 Nginx 配置）：

```bash
docker build -f deploy/frontend/Dockerfile \
  --build-arg APP=admin-web \
  -t ghcr.io/admin-frame/admin-web:<git-sha> .

docker build -f deploy/frontend/Dockerfile \
  --build-arg APP=biz-web \
  -t ghcr.io/admin-frame/biz-web:<git-sha> .
```

生产要求：

- 禁止 `latest`，推送后以 `sha256:<64 hex>` digest 部署（见 `docs/RELEASE_STANDARDS.md`）。
- 镜像内 `config.js` 仅为本地默认值；部署时由 Helm ConfigMap 以 `subPath` 覆盖，**不得**把密钥写入镜像或 ConfigMap。

## 缓存策略

| 路径 | Cache-Control | 原因 |
|---|---|---|
| `/assets/*` | `public, max-age=31536000, immutable` | 文件名含内容哈希；缺失直接 404，不回退 `index.html` |
| `/index.html` | `no-store, no-cache, must-revalidate` | 入口必须即时更新，否则引用已删除的旧 chunk |
| `/config.js` | `no-store, no-cache, must-revalidate` | 环境切换/配置修复必须立即生效 |
| 其他（SPA 回退） | `no-store, no-cache, must-revalidate` | 未知路径返回入口，不缓存 |

若前置 CDN/Nginx Ingress 另有缓存规则，必须保证上述三个路径**不被 CDN 缓存**，否则回滚与配置热修都会失效。

## 本地验证

```bash
docker build -f deploy/frontend/Dockerfile --build-arg APP=admin-web -t admin-web:local .
docker run --rm -p 8080:8080 admin-web:local

curl -sSI http://127.0.0.1:8080/index.html   | grep -i cache-control
curl -sSI http://127.0.0.1:8080/config.js    | grep -i cache-control
curl -sSI http://127.0.0.1:8080/assets/<hash>.js | grep -i cache-control
curl -sS  http://127.0.0.1:8080/healthz
```

预期：入口与配置为 `no-store`，哈希资源为 `immutable`，未知路由返回 200 + `index.html`。

## 发布与回滚

- 安装/升级：见 [`deploy/helm/admin-frame-frontend/README.md`](../helm/admin-frame-frontend/README.md)。
- 回滚步骤：见 [`docs/runbooks/frontend-rollback.md`](../../docs/runbooks/frontend-rollback.md)。
