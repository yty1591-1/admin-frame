# 生产发布 Checklist（RELS-07）

发版负责人在变更窗口前逐项确认。勾选后可将本表链接或摘要贴入发布单。

## 变更元数据

- [ ] 发布单 / 工单号：________
- [ ] 影响端：admin / biz / c / worker / migrate
- [ ] 镜像 tag：________
- [ ] 镜像由 GitHub `Image` 流水线产出（push main / `v*` tag），非本机手工构建
- [ ] 镜像 digest：`sha256:________`（已记录）
- [ ] git commit：________

## 质量门禁

- [ ] 目标 commit 上 CI 全绿（含 arch / secrets / openapi / test）
- [ ] 无已知高危漏洞未缓解（`govulncheck` / 镜像扫描）
- [ ] OpenAPI / 迁移说明已在 MR 写明
- [ ] 契约有变更时：已生成 `make openapi-changelog` 并附入发布说明
- [ ] 已通知前端本次契约的 tag / commit 与 breaking / 弃用项（API_STANDARDS §3.2）

## 迁移（如有）

- [ ] 本次为 expand / 无 DB 变更 / contract（择一）
- [ ] 兼容顺序符合 DATA-05；contract 已确认无旧代码依赖
- [ ] 回滚策略已写明（应用 digest / 是否禁止 down）

## 发布执行

- [ ] staging 冒烟通过
- [ ] 生产 Deployment 使用 tag **且** digest（非 `latest`）
- [ ] 就绪后再接流量（`/readyz`）
- [ ] `/version` 显示预期 version/commit/build_time（非全 `dev`）

## 前端发布（如涉及 admin-web / biz-web）

- [ ] 前端镜像 tag + digest 已记录（`frontend-image` job 产出，非 `latest`）
- [ ] `config.js` 仅含非敏感运行期配置，与目标环境一致
- [ ] `/index.html`、`/config.js` 返回 `no-store`；`/assets/*` 返回 `immutable`
- [ ] 无痕窗口完成登录、刷新与关键路由冒烟
- [ ] 已打开 [frontend-rollback.md](../runbooks/frontend-rollback.md)，上一 Release revision 可回滚

## 回滚预备

- [ ] 上一良好 digest 已备好
- [ ] 已打开 [release-rollback.md](../runbooks/release-rollback.md)
- [ ] 值班与升级频道已通知

## 完成后

- [ ] 监控观察窗口结束，无 P1
- [ ] 若有事故：已开 Postmortem 与 runbook 修订
