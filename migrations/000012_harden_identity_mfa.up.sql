-- MFA 第二因子硬化（IDN-P1-05）：
--   1. last_used_step 记录已消费的 TOTP 时间步，校验时用 CAS 更新以实现防重放；
--   2. version 为绑定行乐观锁，避免并发 Confirm / 解绑 / 恢复码消费互相覆盖。
ALTER TABLE identity_mfa
    ADD COLUMN last_used_step BIGINT NOT NULL DEFAULT 0
        COMMENT '最近一次成功消费的 TOTP 时间步；同一时间步不得重放' AFTER confirmed_at,
    ADD COLUMN version BIGINT NOT NULL DEFAULT 1
        COMMENT '乐观锁版本；确认、消费时间步与密钥重加密时递增' AFTER last_used_step;
