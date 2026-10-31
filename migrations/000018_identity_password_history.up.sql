-- 密码历史与复用限制（IDN-GAP-SEC-06）。
--
-- 设计：
--   identity_credential.password_history 保存最近若干次已退出的密码哈希（JSON 数组），
--   元素为 {hash, algorithm, changed_at}，只存 Argon2id / bcrypt 编码串，不含明文；
--   数量与时间窗口由 identity.password.history_count / history_window 控制。
-- 兼容：列可空，历史数据与手机号凭据不需要回填。
-- 回滚：删除列即可；历史数据不可恢复，但删除不影响当前登录。

ALTER TABLE identity_credential
    ADD COLUMN password_history JSON NULL
        COMMENT '最近密码哈希历史：{hash,algorithm,changed_at}；禁止存明文' AFTER algorithm;
