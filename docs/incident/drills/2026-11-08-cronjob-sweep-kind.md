# 访问复核扫描 CronJob 真实调度演练（kind）— 2026-11-08

| 字段 | 内容 |
|---|---|
| 场景 | P1-7 签收条件 1：`certificationSweep` CronJob 在 Kubernetes 上的**真实调度**与失败行为 |
| 形态 | **真实集群（kind `af-verify`）**，非 staging 业务集群；Alertmanager 路由不在本环境 |
| 日期 | 2026-11-08（UTC 09:56–10:05） |
| 集群 | kind `af-verify-control-plane`，Kubernetes v1.34.3，containerd 2.2.0；helm v4.3.1；chart `admin-frame-0.1.0` |
| 被测对象 | Helm 渲染的 CronJob `af-kind-admin-frame-certification-sweep` → `worker -certification-sweep` 一次性进程 |
| 数据 | 集群库 `admin_frame`：1 个 `active` 活动（`due_at` = 2 小时前）、2 条 `pending` 任务、2 条生效绑定 |
| 结论 | **调度、幂等、失败重试三项成立**；Alertmanager 告警收敛与 staging 环境差异未覆盖 |

## 1. 结论

| 验证点 | 结果 | 证据 |
|---|---|---|
| CronJob 按 `schedule` 真实触发 | **通过**：两次按分钟连续触发，无需手动 `create job` | Job `…-29902197` / `…-29902198` |
| 一次性进程装配成功（DB / Redis / Outbox） | **通过**：`id generator ready` → `sweep finished` | §3.1 日志 |
| 扫描语义：只标记不回收（`auto_revoke=false`） | **通过**：任务 `expired`、绑定未撤销 | §3.2 |
| 幂等：第二轮不重复处理 | **通过**：第二轮 `tasks=0` | §3.1 |
| 审计事件跨进程投递 | **通过**：2 条 `auto_expire` 写入 `async_outbox` 且常驻 worker 投递成功 | §3.3 |
| 失败路径：装配失败 → Job Failed | **通过**：坏 DSN → 3 个 Pod（`backoffLimit=2`）→ Job `Failed` | §4 |
| `concurrencyPolicy: Forbid` 生效 | **配置成立**，未构造重叠场景实测 | §2 坑 1 |
| 告警 `iam.certification.sweep_failed` 收敛 | **未验证**：集群内无 Prometheus / Alertmanager 实例 | §5 |
| staging 业务集群 | **未做**：本环境为 kind 等价集群 | §5 |

## 2. 演练中暴露的两个运维陷阱（真实可复现）

### 陷阱 1：`helm upgrade --reuse-values` 丢弃新增 values 段的默认值

首次升级用 `--reuse-values` 开启 `certificationSweep`，渲染结果：

```text
image:  docker.io/admin-frame/%!s(<nil>):cert-sweep    # repository 变成 nil
concurrencyPolicy: Allow                               # 期望 Forbid，退化为允许并发
```

原因：`--reuse-values` 以**上次 release 的 values** 为基础，而上次 release 里没有 `certificationSweep` 段，
于是 chart 里该段的所有默认值（`repository` / `concurrencyPolicy` / `backoffLimit` / `startingDeadlineSeconds`）
全部丢失，只剩 `--set` 显式给的那几个键。

后果：若不加核对就发布，CronJob 会拿到一个不存在的镜像名（Pod 一直 ImagePullBackOff），
且并发策略变成 `Allow`——上一轮未结束时可能起第二轮，两轮同时回收同一批任务。

正确做法：helm 3.14+/4.x 用 `--reset-then-reuse-values`（保留用户覆盖、重置 chart 默认值），
并在升级后核对三项：

```bash
kubectl -n <ns> get cronjob <name> -o jsonpath='{.spec.schedule}{"\t"}{.spec.concurrencyPolicy}{"\t"}{.spec.jobTemplate.spec.template.spec.containers[0].image}{"\n"}'
```

### 陷阱 2：`kubectl set env` 改动 CronJob 后，`helm upgrade` 会因 value/valueFrom 冲突失败

为验证失败路径给 CronJob 注入坏 `ADMIN_FRAME_DB_DSN`（`kubectl set env`），验证后直接 `helm upgrade` 还原，得到：

```text
Upgrade "af-kind" failed: server-side apply failed for object … Kind=CronJob:
spec.jobTemplate.spec.template.spec.containers[0].env[0].valueFrom: Invalid value: "":
may not be specified when `value` is not empty
```

原因：`set env` 给 env 项加了 `value`，但保留了模板里的 `valueFrom.secretKeyRef`，同一项两个来源并存，
K8s 拒绝；helm 的 server-side apply 因此失败，release 停在 `failed`。

正确做法：**删除 CronJob 对象再 upgrade**（`kubectl delete cronjob … && helm upgrade …`），
让 helm 重建而不是 patch 有冲突的活对象。这两条已写入 `docs/runbooks/iam.md`「季度访问复核」。

## 3. 成功路径证据

### 3.1 两轮调度

```text
# 第一轮 09:57:01Z
{"msg":"certification sweep finished","scanned":1,"overdue":1,"expired":1,"failed":0,"tasks":2,"auto_revoke":false}
# 第二轮 09:58:01Z
{"msg":"certification sweep finished","scanned":1,"overdue":1,"expired":1,"failed":0,"tasks":0,"auto_revoke":false}
```

`scanned=1` 是活动数（扫描以活动为单位）；`tasks` 是被处理的超期任务数。
第二轮 `tasks=0` 说明已过期任务不会被重复计入，扫描对同一批数据幂等。

### 3.2 库内状态

```text
task 900000000000000301  expired  decided_at=2026-11-08 09:57:01.109  decided_by=''
task 900000000000000302  expired  decided_at=2026-11-08 09:57:01.109  decided_by=''
campaign 900000000000000001  active  task_count=2
```

两点与设计一致：

- **绑定未被回收**：`auto_revoke=false` 只标记 `expired`；活动保持 `active` 等待人工关闭，不是自动关闭。
- **`decided_by` 为空**：`Expire` 只写 `status` 与 `decided_at`，操作人只存在于审计事件
  （`auto_expire` 的 actor 是 `system:certification`）——这正是 [验收报告 §3.2](../../iam/p1-7-acceptance-report.md)
  登记的口径，证据消费方必须读审计事件而不是任务列。

### 3.3 审计事件（跨进程投递）

```text
outbox 377758400601145344  operation=auto_expire  subject=drill-staff-1  status=succeeded
outbox 377758400613728256  operation=auto_expire  subject=drill-staff-2  status=succeeded
```

一次性扫描 Pod 不常驻 Relay，事件由常驻 worker 投递——本次实测证明这条「扫描进程写 Outbox、
常驻 worker 投递」的解耦链路在真实集群里闭合（`status=succeeded` 是 worker 投递后的回写）。

## 4. 失败路径证据

注入坏 DSN 后一个调度周期内：

```text
job …-29902199  Failed  0/1  55s
pod …-29902199-fp2hb  Error  (第 1 次)
pod …-29902199-gtpf7  Error  (第 2 次重试)
pod …-29902199-ddx5w  Error  (第 3 次重试)
日志：worker: db: db: ping: dial tcp 127.0.0.1:1: connect: connection refused
```

**失败发生在装配期而非扫描中**：连接不上库时进程 `os.Exit(1)`，不进入扫描逻辑。
`backoffLimit: 2` 产生 1 次初始 + 2 次重试共 3 个 Pod，之后 Job 置 `Failed`。

这一步给出的是告警的**输入信号**：`kube_job_status_failed{job_name=~".*certification-sweep.*"}` 会变为 1。
规则本身的表达式与阈值由 `check-alerts` 门禁校验，不在本演练范围。

## 5. 计时口径

| 计时口径 | 结果 | 说明 |
|---|---|---|
| 检测用时 | **N/A** | 本演练验证调度与扫描行为，不注入故障告警；`iam.certification.sweep_failed` 的检测/确认/缓解三段须在装有 Prometheus 与 Alertmanager 的环境测量（staging，复用 AI-05 窗口） |
| 确认用时 | **N/A** | 同上：失败路径的定位按日志首行（装配错误 vs 扫描错误）区分，属确定性判读，不是告警确认流程 |
| 缓解用时 | **N/A** | 同上：本演练的「缓解」是删除冲突对象后重新 upgrade，用时未作为指标测量 |

> 本记录不声称完成事故响应演练。可核验的事实是「CronJob 按 schedule 真实触发、扫描语义与幂等成立、
> 失败时 Job 置 Failed」，证据以 Job 状态与日志为准。

## 6. 未覆盖的部分

| 项 | 原因 | 后续 |
|---|---|---|
| Alertmanager 告警路由与收敛 | 集群只有 `prometheus-operator-crds`，没有 Prometheus / Alertmanager 实例 | 需 staging 或本地 kube-prometheus-stack |
| staging 业务集群 | 本环境是 kind 等价集群，配置、网络策略、镜像仓库与 staging 不同 | 复用 AI-05 窗口 |
| `concurrencyPolicy: Forbid` 的重叠抑制 | 需要一次扫描跑超过调度间隔 | 用大活动（10 万任务，约 5 s）仍不够；需人为拉长（如注入 sleep 镜像） |
| 镜像 digest 校验 | 本次用本地构建的 `cert-sweep` tag，未走镜像仓库 | 发布流水线覆盖 |
| `auto_revoke=true` 的集群级回收 | 集群配置 `auto_revoke` 为 false（与生产默认一致） | 已在集成层覆盖双路径，staging 再做集群级验证 |

## 7. 复跑方式

镜像：Dockerfile 在容器内 `go mod download` 需要 module proxy，本机网络不可达（构建失败于该步）。
本演练改用本地交叉编译 + 轻量运行镜像，绕开容器内取依赖：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /tmp/wimg/app ./cmd/worker
cp -r migrations /tmp/wimg/
# /tmp/wimg/Dockerfile: FROM alpine:3.24 + adduser 10001 + COPY app/migrations
docker build -t admin-frame/worker:cert-sweep /tmp/wimg
kind load docker-image admin-frame/worker:cert-sweep --name af-verify
```

部署（注意用 `--reset-then-reuse-values`，见 §2 陷阱 1）：

```bash
helm upgrade af-kind deploy/helm/admin-frame -n admin-frame --reset-then-reuse-values \
  --set certificationSweep.enabled=true \
  --set certificationSweep.image.tag=cert-sweep \
  --set-string certificationSweep.schedule="* * * * *"     # 演练用每分钟；结束后改回 0 2 * * *
kubectl -n admin-frame get cronjob af-kind-admin-frame-certification-sweep \
  -o jsonpath='{.spec.schedule}{"\t"}{.spec.concurrencyPolicy}{"\t"}{.spec.jobTemplate.spec.template.spec.containers[0].image}{"\n"}'
```

造数（集群库无生效绑定时需先建角色与绑定，见下方 SQL 摘要）：活动 `status=active`、`due_at` 早于当前时间，
任务 `status=pending` 且外键指向真实 `iam_role_binding`。

```sql
INSERT INTO iam_certification_campaign (id,name,channel,merchant_id,role_ids,privileged_only,status,due_at,created_by,created_at,closed_at,version,task_count)
VALUES (900000000000000001,'CronJob 实战：已超期复核','admin','',JSON_ARRAY('900000000000000101'),0,'active',NOW(3) - INTERVAL 2 HOUR,'system:drill',NOW(3) - INTERVAL 3 HOUR,NULL,1,2);
```

演练结束后把 `schedule` 改回 `0 2 * * *`（本次已恢复，CronJob 保留用于复查）。
