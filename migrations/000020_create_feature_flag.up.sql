-- 000020 (up): 特性开关与灰度（ENT-11）——feature_flag 单表。
--
-- 设计要点：
--   1. 与静态配置的边界：本表存业务功能级开关（秒级、按渠道/租户/百分比
--      差异化）；进程装配形态（iam.mode / async.enabled / db.driver）仍归
--      configs/*.yaml，变更需重启——两者不得互相替代（feature-flag-plan.md §0.2）。
--   2. enabled 是 kill switch：false 时无条件禁用，优先级高于一切规则
--      （「关闭即回退」的落点）；default_enabled 是规则全部未命中的回退值，
--      必须 false（防御性默认，API 层拒绝 true 的裸创建）。
--   3. rules_json 为有序规则数组（channel / tenant_ids / percentage 万分比），
--      首个命中生效；规则校验（越界 / 空串 / 枚举外）在 domain 层拒绝写入。
--   4. version 乐观锁：变更 CAS（version+1），并发写冲突返回 409。
--   5. 到期治理：expires_at 与 permanent 二选一——非 permanent 必须带
--      expires_at（check-featureflag.sh 门禁断言）；到期由 worker 任务自动
--      置 disabled（status 语义），写 featureflag.expire 元审计。
--   6. 评估结果不落库（走 featureflag_* 指标）；不建评估日志表。

CREATE TABLE IF NOT EXISTS feature_flag (
    `key`           VARCHAR(128)  NOT NULL COMMENT 'flag 键（module.feature 小写点分，domain 正则锁定）',
    description     VARCHAR(512)  NOT NULL DEFAULT '' COMMENT '用途说明；permanent flag 须写明常驻理由',
    enabled         TINYINT(1)    NOT NULL DEFAULT 0 COMMENT 'kill switch；0=无条件禁用（优先于一切规则）',
    default_enabled TINYINT(1)    NOT NULL DEFAULT 0 COMMENT '规则全部未命中的回退值；防御性默认 false',
    rules_json      JSON          NOT NULL COMMENT '有序规则数组（channel / tenant_ids / percentage 万分比）',
    permanent       TINYINT(1)    NOT NULL DEFAULT 0 COMMENT '常驻 kill-switch 标记；1=豁免到期治理',
    expires_at      DATETIME(3)   NULL COMMENT '到期时间；permanent=1 时必须为 NULL（门禁断言）',
    version         BIGINT        NOT NULL DEFAULT 1 COMMENT '乐观锁版本（变更 CAS）',
    created_by      VARCHAR(64)   NOT NULL COMMENT '创建主体（操作者或系统标识）',
    created_at      DATETIME(3)   NOT NULL,
    updated_at      DATETIME(3)   NOT NULL,
    PRIMARY KEY (`key`),
    KEY idx_feature_flag_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
    COMMENT='特性开关与灰度（ENT-11）；管理面 A 端权限点 featureflag:read/write';
