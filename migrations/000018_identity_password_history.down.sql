-- 回滚密码历史列。
--
-- 前置：应用已停止读取 / 写入 password_history，避免再次上线时列缺失。
-- 影响：历史密码复用限制失效；当前登录与改密不受影响。

ALTER TABLE identity_credential
    DROP COLUMN password_history;
