## 变更说明

<!-- 做了什么、为什么 -->

## 影响端

- [ ] admin（A）
- [ ] biz（B）
- [ ] c（C）
- [ ] worker / migrate
- [ ] 仅文档 / CI

## 安全自检（SECURITY_STANDARDS）

- [ ] 无密钥、生产连接串、完整 Token 进入代码、配置样例或日志
- [ ] 新接口挂在正确端，且默认鉴权（健康检查白名单除外）
- [ ] B 端校验 `merchant_id`；C 端校验资源归属；响应已裁剪内部字段
- [ ] 无 SQL 拼接 / 命令注入；`auth.demo_mode` 未在生产配置中打开
- [ ] 未新增 pprof / Swagger（或已按 SEC-08 豁免并说明）

## API 自检（API_STANDARDS）

- [ ] 影响端的 `api/openapi/{admin,biz,c}.yaml` 已同步更新
- [ ] 统一信封 / 稳定错误码 / 分页（`page_size`≤100）符合规范
- [ ] 若有 breaking：对应端 `info.version` major 已提升，并写明弃用与迁移
- [ ] C 端写路径含 `Idempotency-Key`；无内部字段进入 C 契约

## 测试自检（TESTING_STANDARDS）

- [ ] 触及的 domain 包有单测；核心 domain 覆盖率 ≥70%
- [ ] B 隔离（`TestMerchantIsolation`）/ C 越权（`TestUserOwnership`）仍通过
- [ ] 未使用生产凭据或生产库跑测试

## 依赖自检（DEPENDENCY_STANDARDS）

- [ ] `go.sum` 已更新；无未审批 `replace`
- [ ] 新直接依赖已说明用途与许可证；`make vuln` / `make check-deps` 可通过

## 配置自检（CONFIG_STANDARDS）

- [ ] 仅更新 `configs/*.yaml.example`；未提交含真实密钥的本地 yaml
- [ ] 新配置项有默认值或 `Validate`；prod 未打开 `demo_mode`

## 数据自检（DATA_STANDARDS）

- [ ] 迁移 up/down 成对；未在 API/worker 自动 migrate
- [ ] 发版顺序（expand/contract）已说明；不可逆已挂 ADR
- [ ] 未将手工改生产库作为常规手段

## 可靠性自检（RELIABILITY_STANDARDS）

- [ ] 新增外部调用带超时 context；无 `http.DefaultClient` / 无超时 Get/Post
- [ ] 写路径幂等明确；限流使用 `42900`
- [ ] 降级路径可观测（日志/指标）

## 可观测自检（OBSERVABILITY_STANDARDS）

- [ ] 日志含 `service`/`channel`/`env`/`trace_id`（请求路径）
- [ ] API 保留 `/healthz`、`/readyz`、`/version`
- [ ] A/B 关键写/导出有审计触发（或已注明后续计划）

## 质量

- [ ] `make ci` 通过（含 gitleaks、gosec、openapi、test/deps/config/DATA/REL/OBS gates）
- [ ] 需要时已更新 ADR / OpenAPI / runbook
