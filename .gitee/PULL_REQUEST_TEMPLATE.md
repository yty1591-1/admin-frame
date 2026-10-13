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

## 质量

- [ ] `make ci` 通过（含 gitleaks 与 gosec）
- [ ] 需要时已更新 ADR / OpenAPI / runbook
