# 前端发布回滚

| 字段 | 内容 |
|------|------|
| 告警 ID | `release.health_failed`（前端侧） |
| 级别 | P1 |
| 服务 / 端 | admin-web / biz-web |
| 最后更新 | 2026-10-26 |

## 症状

- 发布后首屏白屏、`ChunkLoadError`、登录页持续 loading。
- 用户反馈“必须硬刷新才能进”“新版本不生效”。
- 缓存头异常：`index.html`/`config.js` 返回长缓存，或 `/assets/*` 返回 `no-store`。
- `config.js` 的 `apiBaseUrl`/`channel` 与目标环境不一致。

## 影响

- 新会话无法登录或路由 404；旧会话可能继续使用旧 chunk。
- 若 CDN 缓存了 `index.html`，回滚后仍可能短时间返回错误入口。
- 前端发布**不涉及**数据库迁移，回滚无需 `migrate down`。

## 排查

1. 确认当前 Release 与镜像 digest：

   ```bash
   helm history admin-frame-frontend -n admin-frame
   kubectl -n admin-frame get deploy -l app.kubernetes.io/part-of=admin-frame \
     -o custom-columns=NAME:.metadata.name,IMAGE:.spec.template.spec.containers[0].image
   ```

2. 校验缓存头（逐层验证：Ingress → Nginx Pod → CDN）：

   ```bash
   curl -sSI https://<host>/index.html | grep -i -E 'cache-control|age|via'
   curl -sSI https://<host>/config.js  | grep -i -E 'cache-control|age|via'
   curl -sSI https://<host>/assets/<当前入口引用的文件> | grep -i cache-control
   ```

   - 入口/配置出现 `max-age` 或 `age > 0` → 缓存链路问题，优先处理 CDN/Ingress。
   - `/assets/*` 不具备 `immutable` → 镜像内 Nginx 配置未生效。

3. 校对运行期配置：

   ```bash
   kubectl -n admin-frame get cm -l app.kubernetes.io/component=admin \
     -o jsonpath='{.items[0].data.config\.js}'
   ```

4. 定位是应用缺陷还是发布缺陷：对比上一良好 digest 的同类请求；确认失败是否集中在某个 hash 资源（构建不完整）或某个环境（配置错误）。

## 缓解与恢复

1. **镜像回滚**（首选，静态产物与配置一起回到上一版本）：

   ```bash
   helm rollback admin-frame-frontend <REVISION> -n admin-frame --wait --timeout 5m
   kubectl -n admin-frame rollout status deploy/admin-frame-frontend-admin
   kubectl -n admin-frame rollout status deploy/admin-frame-frontend-biz
   ```

2. **仅配置回滚**（镜像正确、`config.js` 错误时）：

   ```bash
   helm upgrade admin-frame-frontend deploy/helm/admin-frame-frontend \
     -n admin-frame -f <上一版 values> --reuse-values --atomic --wait
   ```

   修改 `runtime` 会改变 `checksum/config` 注解，自动触发滚动；无需手工 `kubectl rollout restart`。

3. **清理缓存**：若 CDN 或共享网关缓存了 `index.html`/`config.js`，按供应商流程执行 purge，并复核 `cache-control` 已为 `no-store`。

4. **失败 Pod 不摘流判断**：`maxUnavailable: 0` + readiness `/healthz` 保证发布期间旧副本继续服务；若新副本长期未 Ready，`helm rollback --wait` 会保留旧 ReplicaSet。

## 验证

- `curl -sS https://<host>/healthz` 返回 `ok`。
- 无痕窗口打开首页，`window.__APP_CONFIG__` 与目标环境一致。
- 登录 → 刷新 → 进入 IAM 页，Network 中无 404 chunk。
- 缓存头三条断言全部符合 `deploy/frontend/README.md`。

## 升级

- 回滚后仍白屏 → 检查 Ingress/CDN 与 DNS；升级 L2 Owner + L3 SRE。
- 安全相关（如 CSP、依赖漏洞）发布失败 → 同步安全值班，不得以关闭安全头换取可用性。

## 相关链接

- [ALERTS.md](./ALERTS.md)
- [release-rollback.md](./release-rollback.md)（后端发布回滚）
- [deploy/frontend/README.md](../../deploy/frontend/README.md)
- [frontend-infrastructure-plan.md](../frontend-infrastructure-plan.md)
