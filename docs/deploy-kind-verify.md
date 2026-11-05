# 用 kind 验证 Helm Chart（ENT-05 的可重复执行环境）

在没有 staging 集群时，用 kind 真跑一遍「渲染 → 部署 → 冒烟 → 回滚 → 迁移失败阻断」。
它能发现 `helm lint`、`kubectl --dry-run=client`、甚至 `kubectl --dry-run=server` 都
发现不了的问题——那些只有容器真正启动才暴露的缺陷。

2026-11-02 用这套流程在 3 个半小时内发现并修掉了 4 个既有缺陷（见文末「本轮发现」）。

## 前置

| 工具 | 说明 |
|---|---|
| docker | 本机已装；kind 节点是容器 |
| kind | `curl -fsSL -o kind https://kind.sigs.k8s.io/dl/v0.30.0/kind-windows-amd64` |
| kubectl | Docker Desktop 自带 |
| helm | 3.14+ |
| openssl | 生成 JWT 公钥 |

`prometheus-community` 仓库需要可访问（装 Operator CRD 用）。

## 步骤

### 1. 建集群

```bash
kind create cluster --name af-verify --image kindest/node:v1.34.3 --wait 240s
```

> Windows 上首次创建可能超过 2 分钟。若命令被中途中断但容器已在运行，
> 用 `kind export kubeconfig --name af-verify` 补写 kubeconfig，不必重建。

### 2. 装 Prometheus Operator CRD

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update prometheus-community
helm install prometheus-operator-crds prometheus-community/prometheus-operator-crds \
  -n monitoring --create-namespace --wait
kubectl get crd servicemonitors.monitoring.coreos.com
```

**必须先装**：staging / prod 的 `serviceMonitor.enabled=true` 依赖该 CRD。装之前
`helm install` 会失败，但失败信息是 Chart 自己给的处置指引（不是裸的
`no matches for kind`）。

### 3. 构建并导入镜像

```bash
for t in admin-api biz-api c-api worker migrate; do
  docker build --target $t --build-arg CMD=$t -t admin-frame/$t:verify .
done
kind load docker-image admin-frame/admin-api:verify admin-frame/biz-api:verify \
  admin-frame/c-api:verify admin-frame/worker:verify admin-frame/migrate:verify --name af-verify
```

MySQL / Redis 镜像**不能**用 `kind load`——`mysql:8.0` 是多平台 manifest list，
kind 会报 `content digest ... not found`。改用 `ctr` 导入：

```bash
for img in mysql:8.0 redis:7-alpine; do
  safe=$(echo "$img" | tr ':/' '__')
  docker save "$img" -o "/tmp/$safe.tar"
  docker cp "/tmp/$safe.tar" "af-verify-control-plane:/root/$safe.tar"
  docker exec af-verify-control-plane ctr -n k8s.io images import "/root/$safe.tar"
done
```

> 目标路径必须是 `/root/`，不能是 `/tmp/`：kind 节点内 `/tmp` 由 tmpfs 单独挂载，
> `docker cp` 落不进容器主进程的命名空间。

### 4. 创建命名空间、Secret 与依赖

```bash
kubectl create namespace admin-frame
openssl genrsa -out /tmp/jwt.key 2048
openssl rsa -in /tmp/jwt.key -pubout -out /tmp/jwt.pub
kubectl -n admin-frame create secret generic admin-frame-jwt --from-file=public.pem=/tmp/jwt.pub
```

MySQL / Redis 与三个 Secret（`admin-frame-notification` 的两个值仅供本机 kind 演练，
禁止用于任何非本地环境；staging / prod 必须由外部密钥管理注入）：

```yaml
apiVersion: v1
kind: Secret
metadata: {name: admin-frame-db, namespace: admin-frame}
stringData:
  dsn: "root:verifypass@tcp(mysql.admin-frame.svc.cluster.local:3306)/admin_frame?parseTime=true&loc=UTC&charset=utf8mb4"
---
apiVersion: v1
kind: Secret
metadata: {name: admin-frame-redis, namespace: admin-frame}
stringData: {password: ""}
---
apiVersion: v1
kind: Secret
metadata: {name: admin-frame-notification, namespace: admin-frame}
stringData:
  # c-api 回调验签要求 >=32 字节；worker 黑名单盐只要求非空。
  # 二者缺失会在启动期 fail closed（2026-11-05 kind 实测为 CrashLoopBackOff）。
  callback_secret: "kind-local-callback-secret-0123456789abcdef"
  blacklist_salt: "kind-local-blacklist-salt"
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: mysql, namespace: admin-frame}
spec:
  replicas: 1
  selector: {matchLabels: {app: mysql}}
  template:
    metadata: {labels: {app: mysql}}
    spec:
      containers:
        - name: mysql
          image: mysql:8.0
          imagePullPolicy: IfNotPresent
          env:
            - {name: MYSQL_ROOT_PASSWORD, value: verifypass}
            - {name: MYSQL_DATABASE, value: admin_frame}
          args: ["--character-set-server=utf8mb4", "--collation-server=utf8mb4_unicode_ci"]
          ports: [{containerPort: 3306}]
          readinessProbe:
            exec: {command: ["mysqladmin", "ping", "-uroot", "-pverifypass"]}
            initialDelaySeconds: 10
            periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata: {name: mysql, namespace: admin-frame}
spec:
  selector: {app: mysql}
  ports: [{port: 3306, targetPort: 3306}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: redis, namespace: admin-frame}
spec:
  replicas: 1
  selector: {matchLabels: {app: redis}}
  template:
    metadata: {labels: {app: redis}}
    spec:
      containers:
        - name: redis
          image: redis:7-alpine
          ports: [{containerPort: 6379}]
          readinessProbe:
            exec: {command: ["redis-cli", "ping"]}
            initialDelaySeconds: 5
            periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata: {name: redis, namespace: admin-frame}
spec:
  selector: {app: redis}
  ports: [{port: 6379, targetPort: 6379}]
```

等 `kubectl -n admin-frame get pods` 两个依赖都 `1/1`。

### 5. 渲染校验

```bash
# 离线渲染：必须声明模拟的 API 集合（helm template 不自带 CRD）
helm template af-kind deploy/helm/admin-frame -f deploy/helm/admin-frame/values-kind.yaml \
  --api-versions monitoring.coreos.com/v1 | head -20

# 真集群校验：Helm 从 API server 取真实 API 集合
helm install af-kind deploy/helm/admin-frame -f deploy/helm/admin-frame/values-kind.yaml \
  -n admin-frame --dry-run=server
```

### 6. 部署

```bash
helm install af-kind deploy/helm/admin-frame -f deploy/helm/admin-frame/values-kind.yaml \
  -n admin-frame --timeout 6m
```

**核对迁移先于 API**（T7 的核心保证）：

```bash
kubectl -n admin-frame get pods \
  -o custom-columns='NAME:.metadata.name,CREATED:.metadata.creationTimestamp' \
  --sort-by=.metadata.creationTimestamp | grep af-kind
```

迁移 Pod 的时间必须早于全部 API Pod。2026-11-02 实测：迁移 `13:14:26`，
四个 API 均为 `13:14:30`。

### 7. 冒烟

容器内端口是 **8080**（不是 8081）：

```bash
for p in healthz readyz; do
  kubectl -n admin-frame exec deploy/af-kind-admin-frame-admin -- \
    wget -qO- "http://127.0.0.1:8080/$p"
done
```

期望：

```json
{"status":"ok"}
{"checks":[{"name":"storage","status":"ok"},{"name":"db","status":"ok"},{"name":"redis","status":"ok"},{"name":"iam_db","status":"ok"},{"name":"iam_cache","status":"ok"}],"status":"ok"}
```

biz / c 端同样 `curl /readyz` 应返回 200。

### 8. 回滚与「迁移失败阻断」

```bash
# 升级
helm upgrade af-kind deploy/helm/admin-frame -f deploy/helm/admin-frame/values-kind.yaml \
  -n admin-frame --set workloads.admin.replicas=2 --timeout 6m
# 回滚
helm rollback af-kind 1 -n admin-frame --timeout 6m
kubectl -n admin-frame get deploy | grep af-kind   # 副本数应回到原值
```

验证迁移失败会阻断发布：把 `admin-frame-db` 的 `dsn` 指向不存在的主机，
执行 `helm upgrade --rollback-on-failure --wait`，期望：

```
Error: UPGRADE FAILED: release af-kind failed, and has been rolled back due to
rollback-on-failure being set: pre-upgrade hooks failed: resource Job/...-migrate-4
not ready. status: Failed
```

此时 `kubectl get deploy` 的 Pod AGE 不变（API 未被替换）、`/readyz` 仍返回 ok。
测完记得把 Secret 改回正确 DSN。

## 已知坑

| 现象 | 原因与处置 |
|---|---|
| `ERROR: no matches for kind "ServiceMonitor"` | 未装 Operator CRD；先执行步骤 2 |
| `helm template` 报缺 CRD 而集群已装 | `helm template` 离线渲染不带 CRD，加 `--api-versions monitoring.coreos.com/v1` |
| `kind load` 报 `content digest ... not found` | 多平台镜像；改用 `docker save` + `ctr import` |
| `ctr: open /tmp/x.tar: no such file` | 节点内 `/tmp` 是 tmpfs；`docker cp` 到 `/root/` |
| Job 卡在 Running 但 `get pods` 为空 | pre-install 阶段 ServiceAccount 不存在；SA 必须是 weight `-20` 的 pre-hook |
| `cannot reuse a name that is still in use` | 上次失败的 release 未清理；`helm uninstall <name>` 后重装 |
| `unsupported iam.mode: false` | values 里裸 `off` 被 YAML 1.1 解析成布尔；写成 `"off"` |
| 冒烟连不上 8081 | 容器端口是 8080 |
| c-api / worker CrashLoopBackOff，日志含 `NOTIFICATION_CALLBACK_SECRET` / `NOTIFICATION_BLACKLIST_SALT` | 通知运行时装配期 fail closed；`values-kind.yaml` 已从 Secret `admin-frame-notification` 注入两个变量，步骤 4 必须创建该 Secret（2026-11-05 实测） |
| `ctr: open D:\tmp\... no such file`（Windows） | Windows 版 Docker 把 `/tmp/x.tar` 解析成 `D:\tmp`；`docker save -o` 用 `C:/...` 绝对路径，且 `docker cp`/`docker exec` 的容器内路径要加 `MSYS_NO_PATHCONV=1` 防止 Git Bash 改写 |

## 本轮发现

用这套流程发现的 4 个既有缺陷（均已修复并加门禁）：

1. **`iam.mode: off` 被解析成布尔 false** —— `values-staging.yaml` / `values-prod.yaml`
   各有 2 处，c 端与 worker 部署后直接 CrashLoop。`helm lint` 与 dry-run 都发现不了。
2. **迁移 Job 是 `post-install` hook** —— API Pod 与迁移同一秒创建，新代码在旧 schema 上运行。
   改为 `pre-install,pre-upgrade`，并新增 `migrate-config.yaml`（weight -10）与
   ServiceAccount hook 化（weight -20）。
3. **`ServiceMonitor` 无 CRD 检查** —— `helm install` 抛裸的 `no matches for kind`，
   运维需自行推断要先装 Operator。改为显式判断 + 可读失败信息。
4. **worker 未注入 `deps.IDs`** —— 审计保留服务构造失败导致 CrashLoop；
   `cmd/worker` 自建 Deps 时漏了 ID 生成器（`platform.Run` 有，worker 不走 Run）。

## 清理

```bash
helm -n admin-frame uninstall af-kind
kind delete cluster --name af-verify
docker rmi admin-frame/{admin-api,biz-api,c-api,worker,migrate}:verify
```
