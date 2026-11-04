# 发布回滚

| 字段 | 内容 |
|------|------|
| 告警 ID | `release.health_failed` |
| 级别 | P1 |
| 服务 / 端 | 按发版影响端 |
| 最后更新 | 2026-11-04 |

## 症状

- 发版后 `/readyz` 失败、5xx 激增、关键业务冒烟失败。
- 变更窗口内用户投诉与监控同时告警。

## 影响

- 受影响端流量错误或不可用。
- 若已执行 contract 类迁移，回滚应用**不等于**可安全 down 迁移。

## 排查

1. 确认发布单：镜像 **tag + digest**、变更端、是否含迁移。
2. 对比故障实例 `/version` 与上一良好版本。
3. 区分：应用 bug / 配置错误 / 迁移不兼容 / 依赖故障。

## 缓解与恢复

### 自动化处置（ENT-17，优先走这条）

`.github/workflows/release.yml` 的正常路径自带三层保护，人工介入只在自动化也失败时：

1. **部署阶段**：`helm upgrade --atomic --wait`——迁移 Job 失败、探针不就绪都会回到上一 revision；
2. **金丝雀阶段**：指标门禁（`scripts/ci/release-canary-gate.sh`）不达标时只保留金丝雀、不放大流量；
3. **兜底 job**：`canary` 或 `promote` 失败后自动执行 `rollback` job——卸载 `admin-frame-canary`、
   `helm rollback admin-frame 0`、回滚后冒烟，并把结论写进 Workflow Summary。

### 人工处置命令（自动化失败或被跳过时）

```bash
# 0) 前置：确认在正确的集群与命名空间
kubectl config current-context
export NS=admin-frame

# 1) 看发布历史与当前镜像（确认「上一良好 revision」）
helm history admin-frame -n "$NS" --max 5
kubectl -n "$NS" get deploy -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.template.spec.containers[0].image}{"\n"}{end}'

# 2) 回滚到上一成功 revision（0 = 上一 revision）
helm rollback admin-frame 0 -n "$NS" --wait --timeout 10m

# 3) 回滚到指定 revision（跨多个失败发布时用）
helm history admin-frame -n "$NS" --max 10
helm rollback admin-frame <REVISION> -n "$NS" --wait --timeout 10m

# 4) 清掉金丝雀 release（不清理会让网关权重继续指向旧金丝雀）
helm uninstall admin-frame-canary -n "$NS" --ignore-not-found --wait

# 5) 回滚后冒烟（探针 + 版本一致性 + 未认证拒绝）
bash scripts/ci/release-smoke.sh https://admin.example.com <期望版本>

# 6) 迁移只读探测：确认没有半途迁移，禁止盲目 down
./bin/migrate -action status -driver mysql -dsn "$ADMIN_FRAME_DB_DSN" -dir migrations
```

**迁移**：仅 expand 且旧代码兼容时可保留已执行的迁移；已 contract 或不可逆 → 按 DATA / ADR 处置，
禁止 `migrate down`（回滚应用不等于可回滚 schema）。发布前的 expand 约束由
`scripts/ci/check-migration-gate.sh` 在 preflight 阶段强制。

**业务冒烟**（除脚本自动项外的人工确认）：登录、关键读写、C 端下单幂等路径。

**冻结继续发版直至根因明确**；恢复发布须在 Postmortem 里写明「已修复什么、如何验证」。

## 升级

- 回滚后仍失败 → L2 Owner + L3 SRE；涉及数据 → DBA。
- 安全相关发版问题同步安全值班。

## 相关链接

- [ALERTS.md](./ALERTS.md)
- [RELEASE_STANDARDS.md](../RELEASE_STANDARDS.md)
- [DATA_STANDARDS.md](../DATA_STANDARDS.md)
- [docs/release/CHECKLIST.md](../release/CHECKLIST.md)
