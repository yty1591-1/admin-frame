# 备份、恢复与灾备演练

> 告警 ID：`backup.failed`（P2，由备份 CronJob 失败触发）。
> 目标：**RPO ≤ 5 分钟、RTO ≤ 60 分钟**。
> 现状（2026-11-02 演练）：RTO 达标（全量恢复 3 s / 41 KB 数据）；RPO 由 binlog 归档保证——
> `admin-frame-binlog-archive` CronJob 每 5 分钟归档一次（`deploy/backup/cronjob.yaml`）。
> **生产启用前必须**：配置 `BINLOG_S3_URI`（按实例/集群隔离）、给数据库账号授予
> `REPLICATION SLAVE`（MySQL 8.0.22+ 可用 `BINLOG MONITOR`）与 `RELOAD`。
> 归档 Job 连续失败即 RPO 退化为 24 小时（全备周期），按 `backup.failed` 处理。

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
6. binlog 归档失败（`admin-frame-binlog-archive` Job）：
   - `BINLOG_S3_URI` 是否已配置且**按实例/集群隔离**（留空或前缀复用都会导致失败或覆盖）；
   - 日志是否报权限错误：归档账号需 `REPLICATION SLAVE`（MySQL 8.0.22+ 可用 `BINLOG MONITOR`）
     与 `RELOAD`；
   - 对照 `SHOW BINARY LOGS` 与对象存储清单，确认差异文件数与失败数一致（脚本退出码非 0 即失败）。

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

### binlog 归档（RPO 保障）

CronJob `admin-frame-binlog-archive` 每 5 分钟执行一次，把**已封口**的 binlog 上传到
`BINLOG_S3_URI`，对象键为 `<prefix>/<binlog 文件名>`，并附 `.sha256`。

- **前缀必须按实例/集群隔离**（如 `s3://bucket/prod/cluster-a/`）：实例重建后 binlog 编号
  从 `000001` 重新开始，前缀复用会覆盖历史归档、破坏可恢复性。脚本检测到同名对象大小
  不同会报错退出，不静默覆盖。
- 每次运行先 `FLUSH BINARY LOGS` 封口当前文件；**活跃文件不归档**——它仍在增长，归档后
  会因「同名已存在」在后续轮次被跳过，导致该文件永久缺失后半段写入。
- 幂等：以对象存储现有对象为准判断，CronJob 无状态，重复执行安全。
- 账号权限：`REPLICATION SLAVE`（或 `BINLOG MONITOR`）+ `RELOAD`。
- 手动补归档：

  ```bash
  docker run --rm --network <net> \
    -e DB_DRIVER=mysql -e DB_DSN="$DSN" \
    -e BINLOG_S3_URI="s3://bucket/prod/cluster-a/" \
    -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY \
    <registry>/db-ops:1.0.0 /opt/backup/binlog-archive.sh
  ```

- **归档成功不等于可恢复**：每次更换前缀或重建实例后，按「演练记录 II」做一次
  「仅用归档 binlog 重建空库并比对行数」的验证。

### PITR（MySQL）

**前置条件：执行环境必须有 `mysqlbinlog`。** 官方精简镜像 `mysql:8.0` 只含
`mysqldump` / `mysql` / `mysqlpump`（`rpm -ql mysql-community-server-minimal` 可核对），
**不含 `mysqlbinlog`**，在应用容器或 MySQL 容器内执行都会得到 `command not found`。
本项目提供运维镜像 `deploy/backup/Dockerfile`（含 `mysqlbinlog` / `mysql` / `aws`），
PITR 在该镜像内执行：

```bash
docker run --rm -it --network <net> -e MYSQL_PWD="$DB_PASS" \
  -v /var/backups:/var/backups <registry>/db-ops:1.0.0 bash
# 容器内：
#   mysqlbinlog --start-datetime="$T1" --stop-position="$STOP_POS" "$BINLOG" \
#     | mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "$DB_NAME"
```

> MySQL Shell 8.0.46 的 `util` **不含** `dumpBinlog` / `loadBinlog`（实测成员列表无此项），
> 不能替代 `mysqlbinlog`。

步骤：

1. 恢复最近全量到目标库，记录所用的时间点 T1。
2. 取得 T1 对应的 binlog 文件与位点。全备脚本当前**未记录位点**（`mysqldump` 未加
   `--source-data`，因为现有备份账号无 `RELOAD` 权限），因此必须在**恢复前**执行
   `SHOW MASTER STATUS` 手工记录，或用 `mysqlbinlog --start-datetime=T1` 反查。
3. 抽取并回放增量，`--stop-position` 停在误删语句之前：

   ```bash
   mysqlbinlog --start-datetime="$T1" --stop-position="$STOP_POS" "$BINLOG" \
     | mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "$DB_NAME"
   ```

4. 校验关键表行数与最后写入时间（`SELECT COUNT(*), MAX(created_at)`），通过后再切流量。

> 回放会重放 `DROP` / `TRUNCATE` 等语句：`--stop-position` 必须精确落在目标语句之前，
> 不要用「恢复 + 全量回放」代替，否则会把误删重新执行一遍。

### 对象存储

- 开启版本化（`PutBucketVersioning`）与生命周期（保留 ≥ 30 天）；跨区复制按需开启。
- 误删恢复：按版本 ID 还原，禁止直接删除版本历史。

### 演练机制

- 每季度一次，使用独立环境；记录耗时、恢复点、问题清单，归档到 `docs/runbooks/` 下演练记录。
- 演练完成后更新本 runbook 与 `ALERTS.md` 阈值。
- 演练必须包含「故意制造一次脚本失败」，否则只能验证成功路径。

### 演练记录

#### 2026-11-02 本地全量恢复演练

环境：Docker `mysql:8.0`（8.0.46，`ROW` binlog）+ 6 个迁移 + 1000 条审计事件 + 200 条角色绑定。
宿主机无 MySQL 客户端，用同镜像容器提供 `mysql` / `mysqldump`（参数与生产一致）。

| 步骤 | 结果 | 实测 |
|---|---|---|
| 全量备份（41 KB） | 通过 | 4 s |
| 校验和 + gzip 完整性 | 通过 | < 1 s |
| `verify.sh --load` 载入临时库 | 通过 | 3 s |
| 全量恢复 | 通过 | 3 s |
| 加密备份（gpg AES256）→ 解密恢复 | 通过 | 备份 4 s / 恢复 6 s |
| 27 张表逐表比对：行数 / 列数 / 索引数 | 全部一致（0 不一致） | — |
| `CHECKSUM TABLE` 内容比对 27 表 | 全部一致（0 不一致） | — |
| 恢复后 `migrate -action status` | `current=000006`、`dirty=false`、`pending=none` | — |
| 并发锁（已存在 `.lock` 时再次备份） | 正确拒绝 | — |
| 未设 `RESTORE_CONFIRM` 执行恢复 | 正确拒绝 | — |

结论：**全量恢复 RTO ≈ 3 s（41 KB 数据）**。该数字只能证明链路可用，不能外推生产数据量；
生产 RTO 需按实际备份体积重测（恢复耗时近似与 dump 大小线性相关）。

**演练发现并已修复的缺陷**

| 编号 | 缺陷 | 影响 | 修复 |
|---|---|---|---|
| D1 | `backup.sh` 只认 `mysql://` URL | 项目实际 DSN 是 `user:pass@tcp(host:port)/db`，按 runbook 操作直接报 `DB_NAME: set DB_NAME or use mysql:// URL`，备份从未成功 | 新增 `scripts/backup/dsn.sh` 统一解析两种格式，三个脚本共用 |
| D2 | `restore.sh` / `verify.sh --load` 把 DSN 当数据库名传给 `mysql` | 退化为连接本地 socket，报 `ERROR 2002`，恢复与载入校验不可用 | 拆成 `--host/--port/--user` + 库名，口令走 `MYSQL_PWD` |
| D3 | 加密备份失败时残留完整明文 `.gz` | 「加密备份」失败后备份目录留有可解压的全量明文，被归档或同步出去即为数据泄露 | gpg 失败时删除明文与不完整密文，错误信息说明已清理 |
| D4 | `restore.sh` 的 `trap rm -rf` 失败会改变退出码 | 恢复成功但清理失败时报退出码 1，会误导值班回滚 | `rm -rf ... 2>/dev/null || true` |

以上四项已固化进 `make check-backup`（`dsn.sh` 存在性、三个脚本 `source` 它、
禁止把 DSN 当库名、加密失败必须清理），并做过证伪验证（移除 `source` 行后门禁报错）。

> 复现提示：构造演练数据时注意 `iam_role_binding` 的两个 CHECK 约束——
> `ck_iam_binding_subject_type` 只接受 `staff` / `merchant_staff`，
> `ck_iam_binding_tenant` 要求 `admin` 渠道且 `staff` 时 `merchant_id` 为空串；
> 该表 `id` 无自增，需显式给主键（应用侧由雪花 ID 生成）。

**缺口处理进度**

- **G1（RPO 阻塞项）→ 已实现**。新增 `scripts/backup/binlog-archive.sh`、`admin-frame-binlog-archive`
  CronJob（5 分钟）与运维镜像 `deploy/backup/Dockerfile`（含 `mysqlbinlog` / `aws`）。验证见
  下方「演练记录 II」。生产剩余两项配置：填 `BINLOG_S3_URI`、给数据库账号授
  `REPLICATION SLAVE` + `RELOAD`。
- **G2（PITR 起点）→ 未关闭**。全备仍未记录 binlog 位点：`mysqldump --source-data=2` 需要
  `RELOAD` 权限并会短暂 `FLUSH TABLES WITH READ LOCK`。取舍：先按上文在恢复前手工执行
  `SHOW MASTER STATUS`；若给账号补 `RELOAD`，再在 CronJob 开启并同步更新权限清单。
- **PITR 按时间点截断 → 未验证**。工具（db-ops 镜像）与数据（归档 binlog）已具备，
  「演练记录 II」验证的是全量回放重建；`--stop-datetime` / `--stop-position` 截断需在
  staging 用真实故障时间点演练，本机无稳定时间点可控。

#### 演练记录 II：binlog 归档与灾难重建（2026-11-02）

环境：MySQL 8.0.46（`ROW` binlog，`max_binlog_size=1MB`）+ MinIO（S3 兼容）+ 运维镜像
`db-ops:1.0.0`（545 MB，含 `mysqlbinlog` / `mysql` / `mysqldump` / `aws cli`）。

| 步骤 | 结果 |
|---|---|
| 首次归档：4 个已封口 binlog，共 3,081,688 字节 | 通过；活跃文件被跳过 |
| 二次运行（幂等） | 归档上一轮活跃的 1 个文件，其余 5 个跳过 |
| 对象清单 | 8 个对象（4 binlog + 4 `.sha256`），`sha256sum -c` 全部通过 |
| 仅用归档 binlog 回放到空白实例 | 27 表、绑定 5、审计 3、菜单 20、权限 30、`schema_migrations=6`——与源库逐项一致 |

结论：归档链路可用于「实例完全损坏」场景的灾难重建，且不依赖实例本地文件。

**镜像构建**：`docker build -f deploy/backup/Dockerfile -t <registry>/db-ops:<ver> .`
（上下文必须是仓库根，脚本随镜像分发）。构建时用 `mysql:8.0-debian` 作为客户端来源、
只拷三个客户端二进制到 `debian:bookworm-slim`，避免引入数据库服务端；`aws cli v2`
按 `TARGETARCH` 选择 x86_64 / aarch64 安装包。

## 升级

- 备份连续失败 ≥ 2 个周期，或发现备份不可恢复：升级 L2（DBA/平台 Owner）。
- 涉及生产数据丢失、恢复失败或误删：立即升级 L3（SRE/架构 Oncall）并按安全事件流程处理。
- P1 恢复窗口超过 30 分钟无进展即升级。

## 相关链接

- 脚本：[`scripts/backup/backup.sh`](../../scripts/backup/backup.sh)、[`restore.sh`](../../scripts/backup/restore.sh)、[`verify.sh`](../../scripts/backup/verify.sh)、[`dsn.sh`](../../scripts/backup/dsn.sh)（DSN 解析，三个脚本共用）
- 部署：[`deploy/backup/cronjob.yaml`](../../deploy/backup/cronjob.yaml)（全量备份 + binlog 归档两个 CronJob）
- 运维镜像：[`deploy/backup/Dockerfile`](../../deploy/backup/Dockerfile)（含 `mysqlbinlog` / `aws cli`，PITR 与归档共用）
- binlog 归档脚本：[`scripts/backup/binlog-archive.sh`](../../scripts/backup/binlog-archive.sh)
- 迁移：[`docs/migration-guide.md`](../migration-guide.md)
- 发布回滚：[`release-rollback.md`](./release-rollback.md)
- 数据规范：[`docs/DATA_STANDARDS.md`](../DATA_STANDARDS.md)
