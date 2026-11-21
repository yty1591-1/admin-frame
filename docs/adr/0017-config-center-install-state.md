# ADR-0017：配置中心 prod-like 首次安装态

- 状态：Accepted（已接受，2026-11-21）
- 日期：2026-11-21
- 关联：[ADR-0013](0013-config-center-in-process-snapshot.md)、[ADR-0016](0016-config-service-pull-model.md)、[`../CONFIG_STANDARDS.md`](../CONFIG_STANDARDS.md) §4、[`../config-center-plan.md`](../config-center-plan.md) D16

## 背景

ADR-0013 / D16 规定空库按 key spec **自动播种**，不做安装引导。prod/staging 下这会：

- 把业务侧 db/redis/storage/JWT 等以 overlay / 占位密钥写入，运维无法在首次投产时确认；
- 配置中心启动与「业务配置是否就绪」耦合过紧。

需要区分：**config-api 进程自举**（自身 DB / env / mTLS / KEK）与 **业务配置播种**（可延后到安装向导）。

## 决策

1. **local/dev/test**：保持启动期 `EnsureBaseline` + `EnsureAutoSecrets`，并将 `config_install_state` 标为 `ready`。
2. **staging/prod（prod-like）**：启动只做最小自举；跳过自动业务播种；`config_install_state.status=pending`。
3. **安装真相**以表 `config_install_state` 为准（`pending|installing|ready|failed`）；`config_release_pointer` 仅作 `complete` 校验。
4. **写路径鉴权**用部署注入的 `CONFIG_INSTALL_SETUP_TOKEN`（请求头 `X-Config-Install-Token`）；`ready` 后拒绝写接口。Token 不入库。
5. **安装 API**挂在 config-api：`GET/POST /internal/v1/install/{status,begin,apply,complete}`。Admin SPA 经 **admin-api** 同源代理 `/admin/v1/install/*`（admin-api 用 mTLS 客户端转发）；浏览器不直连 config-api（prod mTLS 无法出示 PEM）。安装期 admin-api 可启动但 `/readyz` 失败，本地 Vite 仍可直连 `:8081`。
6. **集群互斥**：`begin` 以 CAS + 租约（默认 15m）抢 `installing`；无 leader election。
7. **就绪语义**：config-api 在 pending/installing 时 `/readyz` 仍成功（可装）；admin/biz/c/worker 注册 `config_install` 检查，未 `ready` 则 `/readyz` 503。业务进程允许启动（安装期用代码默认值 + 占位密钥完成监听）。

## 后果

**正面**：prod 首次配置由人确认；dev 体验不变；安装目标可达。

**负面**：prod 演练 / 首次部署多一步安装；业务进程在未安装时进程活着但不出流量。

**守卫**：迁移成对、OpenAPI、审计事件、`check-configcenter.sh` 断言 install 端点与状态机。

## 取代关系

- 修订 config-center-plan **D16**：prod-like 改为安装向导；dev 仍自动播种。
- 不取代 ADR-0016 拉取模型；仅改变首次播种触发点。
