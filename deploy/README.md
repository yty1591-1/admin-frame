# 部署说明

完整的技术栈清单、资源基线、生产必配项与上线检查清单见 [tech-stack-deployment.md](./tech-stack-deployment.md)。

| 交付物 | 说明 |
|---|---|
| [helm/admin-frame](./helm/admin-frame/README.md) | 后端 Helm Chart：三端 API + worker + migrate Job、探针、HPA、PDB、NetworkPolicy、ServiceMonitor |
| [helm/admin-frame-frontend](./helm/admin-frame-frontend/README.md) | 前端 Helm Chart：A/B 端静态站点、非 root Nginx、运行期 `config.js`、Ingress/PDB |
| [frontend](./frontend/README.md) | 前端镜像与 Nginx 缓存策略（`index.html`/`config.js` no-store，`/assets/*` immutable） |
| [observability](./observability/README.md) | OTel Collector、Prometheus 告警规则、SLO、Alertmanager、Grafana 看板 |
| [backup](./backup/cronjob.yaml) | 数据库备份 CronJob（脚本见 `scripts/backup/`） |

建议：

- `admin-api` / `biz-api` / `c-api` 各自 Service + Ingress 前缀 `/admin` `/biz` `/c`
- readiness 探针 `/readyz`，liveness 探针 `/healthz`；worker 不监听 HTTP，依赖积压告警
- 前端与后端 **独立发布**：前端 Workloads 只含静态站点；同源部署时 Ingress 必须先按 API 前缀转发，避免 SPA 回退吞掉接口请求
- 密钥通过 Secret / 外部密钥管理注入，勿写入镜像；前端 `config.js` 只允许非敏感运行期配置
- 生产发布使用 `helm upgrade --install --atomic --wait`，后端迁移失败自动回滚；前端回滚见 [frontend-rollback.md](../docs/runbooks/frontend-rollback.md)
