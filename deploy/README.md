# 部署说明

完整的技术栈清单、资源基线、生产必配项与上线检查清单见 [tech-stack-deployment.md](./tech-stack-deployment.md)。

| 交付物 | 说明 |
|---|---|
| [helm/admin-frame](./helm/admin-frame/README.md) | Helm Chart：三端 API + worker + migrate Job、探针、HPA、PDB、NetworkPolicy、ServiceMonitor |
| [observability](./observability/README.md) | OTel Collector、Prometheus 告警规则、SLO、Alertmanager、Grafana 看板 |
| [backup](./backup/cronjob.yaml) | 数据库备份 CronJob（脚本见 `scripts/backup/`） |

建议：

- `admin-api` / `biz-api` / `c-api` 各自 Service + Ingress 前缀 `/admin` `/biz` `/c`
- readiness 探针 `/readyz`，liveness 探针 `/healthz`；worker 不监听 HTTP，依赖积压告警
- 密钥通过 Secret / 外部密钥管理注入，勿写入镜像
- 生产发布使用 `helm upgrade --install --atomic --wait`，迁移失败自动回滚
