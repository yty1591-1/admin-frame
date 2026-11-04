-- 000017 (up): 通知模板种子（ENT-10 NT-09）——租户邀请邮件（T-14 接入）。
--
-- 设计要点：
--   1. 租户名 / 被邀请人 / 接受链接由编排层在投递时注入（params），
--      库里只存模板骨架；变量占位符集合即渲染白名单（缺参 / 多参拒绝）。
--   2. 场景分类 transactional：偏好层不可退订——邀请是账号安全动作。
--   3. 首版本 version=1；模板内容变更走管理端更新（version 自增），
--      渲染按版本锁定，重试期间内容不漂移。
--   4. 幂等写入：主键冲突时回写内容（ON DUPLICATE KEY UPDATE），
--      迁移重放安全（check-migrations DATA 幂等门禁）。

INSERT INTO notification_template
    (id, template_key, channel, locale, version, subject, body, enabled, created_by, created_at, updated_at)
VALUES
    ('seed-invitation-email-zh', 'tenant.invitation', 'email', 'zh-CN', 1,
     '【{{tenant_name}}】您被邀请加入租户',
     '您好：\n\n{{tenant_name}} 邀请您以受邀身份加入其工作空间。\n\n请在以下链接完成接受（链接将于 {{expires_at}} 前有效）：\n{{invite_url}}\n\n如果您不认识发起邀请的组织，请忽略本邮件或联系该组织管理员。\n\n—— 本邮件由系统发送，请勿直接回复',
     1, 'system', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
ON DUPLICATE KEY UPDATE
    subject     = VALUES(subject),
    body        = VALUES(body),
    enabled     = VALUES(enabled),
    created_by  = VALUES(created_by),
    updated_at  = VALUES(updated_at);
