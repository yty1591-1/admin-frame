# ADR-0009：特性开关采用进程内评估与 TTL 缓存，不引入外部 flag 平台

- 状态：Accepted
- 日期：2026-11-04
- 关联：[`../feature-flag-plan.md`](../feature-flag-plan.md)（ENT-11 规划）、[ADR-0007](0007-tenant-member-iam-binding.md)（消费方定义端口先例）、[ADR-0008](0008-notification-event-driven.md)（唯一例外先例的固化方式）

## 背景

`ARCHITECTURE.md` §17.3 要求高风险逻辑用 feature flag（默认关闭、按渠道 / 商户 / 百分比灰度、可观测、定期清理）。ENT-11 规划（`feature-flag-plan.md`）需要选定评估架构：进程内实现还是外部 flag 平台。

## 决策

1. **进程内评估，零外部依赖**：`internal/featureflag` 自建注册表 + 规则引擎 + 稳定哈希分桶；不引入 LaunchDarkly / Unleash / Nacos 等外部平台。理由：
   - 判据是「秒级开关、关闭即回退」——5s TTL 缓存即可满足，不需要外部平台的亚秒推送；
   - 外部平台引入新的可用性依赖与凭据面（对齐 ENT-07 的评审逻辑：能用既有设施满足就不新增组件）；
   - flag 数据单表 + 单一评估语义，自建成本低且审计链路（`featureflag.*`）与平台审计体系天然一致。
2. **两级 TTL 缓存，不做跨副本推送失效**：kill switch 面 5s、规则面 30s。多副本一致性靠 TTL 自然收敛——判据是秒级，不是毫秒级；更强的时效需求（安全热修）走部署回滚，由「高风险逻辑默认带 flag」的规范保证 flag 存在。
3. **消费方定义端口**：业务域（如 tenant）定义自己的窄评估接口，runtime 注入 featureflag 实现；业务域不 import featureflag 内部（对齐 ADR-0007 的 InvitationNotifier 先例）。未装配时注入 noop（保持最小部署形态可运行）。
4. **评估不落库**：评估结果走 `featureflag_*` 指标；不建评估日志表（flag 评估是热路径，落库即瓶颈；审计只覆盖管理面变更）。
5. **唯一评估语义是布尔**：明确不做多 variant / A-B 实验——需要实验平台时另立 ADR 评审（防 scope creep）。

## 后果

- **正面**：零新增部署组件；审计 / 权限 / 门禁复用既有体系；评估路径缓存命中时零 I/O。
- **负面**：跨副本变更生效有 ≤5s（kill switch）/ ≤30s（规则）延迟——已写入 runbook 与 API 响应字段（`effective_within_seconds`）。
- **守卫**：`check-featureflag.sh` 门禁（key 命名 / 审计对账 / 幽灵 key / 到期治理）防止 flag 体系腐化；「无长期遗留 flag」由 expires_at 强制 + 到期任务闭环。
