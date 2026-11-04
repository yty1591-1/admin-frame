# 生产发布 Checklist（RELS-07）

发版负责人在变更窗口前逐项确认。勾选后可将本表链接或摘要贴入发布单。

## 变更元数据

- [ ] 发布单 / 工单号：________
- [ ] 影响端：admin / biz / c / worker / migrate
- [ ] 镜像 tag：________
- [ ] 镜像由 GitHub `Image` 流水线产出（push main / `v*` tag），非本机手工构建
- [ ] 镜像 digest：`sha256:________`（已记录）
- [ ] SBOM：`sbom-<cmd>` artifact 已生成并可下载（CycloneDX），且 attestation 挂在**同一 digest** 上
- [ ] 验签：`Release` 流水线 `verify` job 通过（keyless 身份 = 本仓库 `image.yml`），无 `continue-on-error`
- [ ] git commit：________

## 质量门禁

- [ ] 目标 commit 上 CI 全绿（含 arch / secrets / openapi / test）
- [ ] 无已知高危漏洞未缓解（`govulncheck` / 镜像扫描）
- [ ] OpenAPI / 迁移说明已在 MR 写明
- [ ] 契约有变更时：已生成 `make openapi-changelog` 并附入发布说明

## 迁移（如有）

- [ ] 本次为 expand / 无 DB 变更 / contract（择一）
- [ ] 兼容顺序符合 DATA-05；contract 已确认无旧代码依赖
- [ ] `Release` 流水线的 preflight 已通过（`check-migration-gate`：expand-only / append-only / dirty=false）
- [ ] 回滚策略已写明（应用 digest / 是否禁止 down）

## 发布执行

- [ ] `Release` 流水线由 `v*` tag 或 `workflow_dispatch`（dry_run 先演练一次）触发
- [ ] staging 阶段冒烟通过（`release-smoke.sh`：探针 / 版本一致 / 未认证 401）
- [ ] 金丝雀阶段指标门禁通过（流量下限 / 5xx / p95），且金丝雀确实接到流量
- [ ] 生产 Deployment 使用 tag **且** digest（非 `latest`）
- [ ] 就绪后再接流量（`/readyz`）
- [ ] `/version` 显示预期 version/commit/build_time（非全 `dev`）
- [ ] 金丝雀 release（`admin-frame-canary`）已在 promote 阶段卸载
- [ ] 生产实际运行的 digest 与「已验签的 digest」一致（`kubectl get deploy -o jsonpath` 抽查一端）

## 回滚预备

- [ ] 上一良好 digest 已备好（`helm history admin-frame` 可查）
- [ ] 确认流水线 `rollback` job 可用；否则按 runbook 手工 `helm rollback`
- [ ] 已打开 [release-rollback.md](../runbooks/release-rollback.md)
- [ ] 值班与升级频道已通知

## 完成后

- [ ] 监控观察窗口结束，无 P1
- [ ] 若有事故：已开 Postmortem 与 runbook 修订
