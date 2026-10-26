-- 回滚身份域模块表；顺序与外键依赖相反，先删子表再删账号表。
-- 数据不可恢复：回滚前必须完成账号 / 会话数据导出（docs/identity-authn.md §17）。

DROP TABLE IF EXISTS identity_oauth_identity;
DROP TABLE IF EXISTS identity_login_attempt;
DROP TABLE IF EXISTS identity_mfa;
DROP TABLE IF EXISTS identity_session;
DROP TABLE IF EXISTS identity_credential;
DROP TABLE IF EXISTS identity_account;
