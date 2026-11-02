-- ENT-04 T1：审计表晋升为平台级 audit_event。
--
-- 原表 identity_audit_event 是身份域先行落地的追加写表；IAM 与后续业务域的
-- 审计事件同样写入该表，按域命名已与实际用途不符。本迁移只做重命名，
-- 不改列与数据，保证既有审计记录连续可查（AUD-06 追加写、AUD-07 保留期）。
--
-- 注意：重命名后应用侧默认表名同步为 audit_event（internal/shared/audit 与
-- internal/audit/infrastructure/persistence），回滚脚本会改回原名。

RENAME TABLE identity_audit_event TO audit_event;

-- 查询侧新增：按动作检索（审计排查最常见的入口）与按端别检索。
ALTER TABLE audit_event
    ADD KEY idx_audit_action (action, occurred_at) COMMENT '按动作 + 时间检索',
    ADD KEY idx_audit_channel (channel, occurred_at) COMMENT '按端别 + 时间检索';
