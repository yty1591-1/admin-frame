# 备份、恢复与灾备演练

> 告警 ID：`backup.failed`（P2，由备份 CronJob 失败触发）。
> 目标：**RPO ≤ 5 分钟、RTO ≤ 60 分钟**。
> 现状（2026-11-02 演练）：RTO 达标（全量恢复 3 s / 41 KB 数据）；RPO 由 binlog 归档保证——
> `admin-frame-binlog-archive` CronJob 每 5 分钟归档一次（`deploy/backup/cronjob.yaml`）。
> **生产启用前必须**：配置 `BINLOG_S3_URI`（按实例/集群隔离）、给数据库账号授予
> `REPLICATION SLAVE`（MySQL 8.0.22+ 可用 `BINLOG MONITOR`）、`RELOAD` 与
> `REPLICATION CLIENT`。后两项是**全备记录 binlog 位点**的前提：`RELOAD` 用于
> `FLUSH TABLES` 取得与快照一致的位点，`REPLICATION CLIENT` 用于读 `SHOW MASTER STATUS`。
> 缺任一项 `mysqldump --source-data` 会直接以非 0 退出（8.0.46 实测），备份 CronJob 随之失败。
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
2. 检查 `DB_DSN` Secret 是否有效、数据库账号是否具备
   `SELECT, LOCK TABLES, SHOW VIEW, EVENT, TRIGGER, RELOAD, REPLICATION CLIENT`。
   后两项缺失时日志报 `did not record a binlog position` 并以非 0 退出——
   这是**故意失败**：没有位点的备份无法前滚，不如让它可见地失败。
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
./bin/migrate -config configs/prod/admin.yaml -action status
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
PITR 在该镜像内执行。MySQL Shell 8.0.46 的 `util` **不含** `dumpBinlog` / `loadBinlog`
（实测成员列表无此项），不能替代 `mysqlbinlog`。

#### 起点由备份自己带上（G2，2026-11-02 起）

`backup.sh` 调用 `mysqldump --source-data=2`，把一致性快照对应的 binlog 位点写进
dump 头部注释，并另存 `<备份文件>.pos`（含 `BINLOG_FILE` / `BINLOG_POS` 等字段）。
**不再需要在恢复前手工 `SHOW MASTER STATUS`。**

解析不到位点时备份直接失败（`BACKUP_REQUIRE_POSITION=1`，默认）：没有位点的全备
只能恢复到备份瞬间，事故后写入全部丢失，而这种备份在监控里「存在且可解压」，
会被当成可用备份——失败比静默失效安全。

`.pos` 会随备份一起上传对象存储。**只上传备份而漏掉 `.pos`，异地恢复同样无法前滚**，
门禁 `make check-backup` 有断言。

#### 操作步骤

1. 确认备份与位点文件齐全：

   ```bash
   ls -1 /var/backups/mysql-*.dump.gz /var/backups/mysql-*.dump.gz.pos
   cat /var/backups/mysql-<TIMESTAMP>.dump.gz.pos   # BINLOG_FILE / BINLOG_POS
   ```

2. 先 dry-run，核对回放范围（只恢复全量、打印 `mysqlbinlog` 参数、不回放）：

   ```bash
   RESTORE_CONFIRM=yes PITR_DRY_RUN=1 PITR_STOP_POS=<STOP_POS> \
   DB_DRIVER=mysql DB_DSN='<目标库 DSN>' \
     /opt/backup/restore.sh /var/backups/mysql-<TIMESTAMP>.dump.gz
   ```

   日志中出现 `PITR range: mysql-bin.0000NN:<起点> -> mysql-bin.0000MM:<STOP_POS>`，
   确认起点等于备份位点、终点是误操作之前。

3. 取值并正式执行。`PITR_STOP_POS` 与 `PITR_STOP_DATETIME` **只能给一个**：

   ```bash
   # 按位点（精确到字节，首选）
   RESTORE_CONFIRM=yes PITR_CONFIRM=yes \
   PITR_STOP_FILE=mysql-bin.0000MM PITR_STOP_POS=<STOP_POS> \
   DB_DRIVER=mysql DB_DSN='<目标库 DSN>' \
     /opt/backup/restore.sh /var/backups/mysql-<TIMESTAMP>.dump.gz

   # 按时刻（UTC，精度到秒；避开与误操作同一秒）
   RESTORE_CONFIRM=yes PITR_CONFIRM=yes PITR_STOP_DATETIME='2026-11-02 12:30:39' \
   DB_DRIVER=mysql DB_DSN='<目标库 DSN>' \
     /opt/backup/restore.sh /var/backups/mysql-<TIMESTAMP>.dump.gz
   ```

   `PITR_STOP_FILE` 省略时默认取服务器上最后一个 binlog。起点文件与位点省略时
   从 `<备份文件>.pos` 读取；可用 `PITR_START_FILE` / `PITR_START_POS` 显式覆盖。

4. 校验关键表行数与最后写入时间（`SELECT COUNT(*), MAX(occurred_at)`），通过后再切流量。

#### 为什么需要两个确认

- `RESTORE_CONFIRM=yes`：确认恢复目标环境（恢复全量会覆盖目标库）。
- `PITR_CONFIRM=yes`：确认**回放**。回放会把 binlog 里的 `DELETE` / `DROP` / `TRUNCATE`
  一并重放，`stop` 位点算错等于造成第二次数据破坏。只恢复全量时不需要它。

#### 参数预检在恢复之前

`restore.sh` 在覆盖目标库**之前**完成全部 PITR 校验：参数互斥与格式、`.pos` 是否可用、
`mysqlbinlog` 是否存在、`SHOW BINARY LOGS` 中起止文件是否存在且顺序合理。
参数错误时不会先毁掉目标库再报错（门禁 `check-backup` 用行号断言锁住这个顺序）。

#### 回放失败怎么办

脚本会明确报 `database is now partially rolled forward` 并列出常见原因。**不要重跑
同一命令**——已成功应用的语句会被重复执行。正确处置是丢弃目标库、从全量恢复重来，
并检查 `.pos` 是否与这份备份匹配（常见错配是把 A 备份的 `.pos` 配给了 B 备份）。

> 回放会重放 `DROP` / `TRUNCATE` 等语句：`PITR_STOP_POS` 必须精确落在目标语句之前，
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
- **G2（PITR 起点）→ 已关闭（2026-11-02）**。`backup.sh` 加 `--source-data=2`，位点写进 dump
  头并另存 `<备份>.pos`；缺失时备份失败而非静默产出不可前滚的备份。权限实测结论：
  需要 **`RELOAD` + `REPLICATION CLIENT`** 两项（原先只记录了 `RELOAD`，缺
  `REPLICATION CLIENT` 时 `SHOW MASTER STATUS` 被拒）。验证见「演练记录 III」。
- **PITR 按时间点截断 → 已验证（2026-11-02）**。`restore.sh` 实现 `PITR_STOP_POS`（字节精确）
  与 `PITR_STOP_DATETIME`（秒级）两种截断，含跨 binlog 文件回放。两种方式均端到端跑通，
  证明「备份后增量被找回、误删未被重放」。见「演练记录 III」。

#### 演练记录 III：PITR 位点记录与截断回放（2026-11-02）

环境：MySQL 8.0.46（`ROW` binlog，`max_binlog_size=1MB`）+ 运维镜像 `db-ops:1.0.0`，
全部操作在运维镜像内执行（生产中 PITR 也必须在该镜像内，宿主机无 `mysqlbinlog`）。

场景：T1 全备 → T2 写入增量（5 条绑定 + 3 条审计）→ T3 取断点 → T4 误删 baseline 10 条
→ `DROP DATABASE` 模拟灾难 → 全量恢复 + 回放到 T3。

| 检查项 | 期望 | 实测 |
|---|---|---|
| 全备记录位点 | `mysql-bin.000004:85923` 写入 `.pos` | 通过 |
| 仅全量恢复后 | binding=10、增量=0 | 10 / 0 |
| **回放后增量找回** | 5 条 `new-staff-*` + 3 条 `evt-incr-*` | 5 / 3 |
| **误删未被重放** | baseline 10 条完好 | 10 |
| 跨文件回放（000004 → 000005） | 两个文件都回放、终点精确 | 通过，14 s |
| 按时刻截断（`--stop-datetime`） | 误删（断点后 2 s）未被回放 | 通过，增量 5 条找回 |
| 表结构 | 27 表 | 27 |

失败路径（均验证为「在覆盖目标库之前」拒绝，用 canary 行证明库未被改动）：

| 输入 | 结果 |
|---|---|
| 参数互斥：同时给 `PITR_STOP_POS` 与 `PITR_STOP_DATETIME` | 拒绝，canary 行幸存 |
| 缺 `.pos` 且未显式传起点 | 拒绝，canary 行幸存 |
| `PITR_CONFIRM` 未设置 | 拒绝 |
| 起始 binlog 不在服务器上 | 拒绝，提示检查 `binlog_expire_logs_seconds` 与归档前缀 |

回放失败的处理（用错误起始位点制造 `Duplicate entry`）：脚本报
`database is now partially rolled forward` 并列出常见原因，明确要求丢弃目标库重来。

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
