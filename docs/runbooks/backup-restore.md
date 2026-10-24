# 备份、恢复与灾备演练

> 告警 ID：`backup.failed`（P2，由备份 CronJob 失败触发）。
> 目标：**RPO ≤ 5 分钟、RTO ≤ 60 分钟**（待业务确认后固化）。

## 症状

- 备份 CronJob 失败（`kubectl -n admin-frame get jobs -l app.kubernetes.io/component=backup`）。
- 备份目录无新文件、校验和不匹配或 `verify.sh` 退出非 0。
- 恢复演练时发现备份不可读、缺少表或数据明显缺失。
- 对象存储出现误删且未开启版本化。

## 影响

- 备份缺失意味着**数据丢失风险**，不影响在线请求，但会阻断发布回滚与合规要求。
- 生产 RPO 依赖 MySQL binlog 保留期（建议 ≥ 7 天）与每日全量；RTO 取决于备份大小与恢复环境。
- 单租户数据误删可按 SQL 模板定向修复，避免全库回滚。

## 排查

1. 确认 Job 状态与日志：
   ```bash
   kubectl -n admin-frame get cronjob admin-frame-db-backup
   kubectl -n admin-frame logs job/$(kubectl -n admin-frame get jobs -l app.kubernetes.io/component=backup -o jsonpath='{.items[-1].metadata.name}')
   ```
2. 检查 `DB_DSN` Secret 是否有效、数据库账号是否具备 `SELECT, LOCK TABLES, SHOW VIEW, EVENT, TRIGGER`。
3. 检查 PVC 容量（`df -h /var/backups/admin-frame`）与对象存储上传凭据。
4. 手动执行 `backup.sh` 与 `verify.sh`，确认是环境问题还是脚本问题。
5. 检查 binlog 保留：`SHOW VARIABLES LIKE 'binlog_expire_logs_seconds';`

## 缓解与恢复

### 全量恢复（staging 演练优先）

```bash
# 1) 摘流：API 副本置 0 或 Ingress 下线；worker 缩容为 0
# 2) 备份当前库（即使已损坏，保留现场）
RESTORE_CONFIRM=yes DB_DRIVER=mysql DB_DSN="$SAFETY_DSN" \
  bash scripts/backup/backup.sh
# 3) 恢复到新库（生产禁止直接覆盖）
RESTORE_CONFIRM=yes DB_DRIVER=mysql DB_DSN="$RESTORE_DSN" \
  bash scripts/backup/restore.sh /var/backups/admin-frame/mysql-<ts>.dump.gz
# 4) 校验与迁移状态
DB_DRIVER=mysql DB_DSN="$RESTORE_DSN" bash scripts/backup/verify.sh <file> --load
./bin/migrate -config configs/admin.yaml -action status
# 5) 切换连接串 → 恢复 worker → 灰度 API
```

### PITR（MySQL）

1. 恢复最近全量到临时库。
2. 用 `mysqlbinlog --start-datetime=... --stop-datetime=...` 抽取增量并回放。
3. 校验关键表行数与最近写入时间，再切换。

### 对象存储

- 开启版本化（`PutBucketVersioning`）与生命周期（保留 ≥ 30 天）；跨区复制按需开启。
- 误删恢复：按版本 ID 还原，禁止直接删除版本历史。

### 演练机制

- 每季度一次，使用独立环境；记录耗时、恢复点、问题清单，归档到 `docs/runbooks/` 下演练记录。
- 演练完成后更新本 runbook 与 `ALERTS.md` 阈值。

## 升级

- 备份连续失败 ≥ 2 个周期，或发现备份不可恢复：升级 L2（DBA/平台 Owner）。
- 涉及生产数据丢失、恢复失败或误删：立即升级 L3（SRE/架构 Oncall）并按安全事件流程处理。
- P1 恢复窗口超过 30 分钟无进展即升级。

## 相关链接

- 脚本：[`scripts/backup/backup.sh`](../../scripts/backup/backup.sh)、[`restore.sh`](../../scripts/backup/restore.sh)、[`verify.sh`](../../scripts/backup/verify.sh)
- 部署：[`deploy/backup/cronjob.yaml`](../../deploy/backup/cronjob.yaml)
- 迁移：[`docs/migration-guide.md`](../migration-guide.md)
- 发布回滚：[`release-rollback.md`](./release-rollback.md)
- 数据规范：[`docs/DATA_STANDARDS.md`](../DATA_STANDARDS.md)
