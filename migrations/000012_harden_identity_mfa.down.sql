-- 回滚 MFA 硬化列（IDN-P1-05）。
ALTER TABLE identity_mfa
    DROP COLUMN version,
    DROP COLUMN last_used_step;
