# 部署说明

三端 API 与 worker 可独立 Deployment / HPA。示例清单可按环境补充 Helm 或 Kustomize。

建议：

- `admin-api` / `biz-api` / `c-api` 各自 Service + Ingress 前缀 `/admin` `/biz` `/c`
-  readiness 探针 `/readyz`，liveness 探针 `/healthz`
- 密钥通过 Secret / 外部密钥管理注入，勿写入镜像
