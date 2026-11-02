-- 回滚身份域模块基线（结构合并版）；顺序与外键依赖相反，先删子表再删账号表。
-- 归档表与审计表不建外键，但在线表仍被其语义引用，一并删除以保持基线对称。
-- 数据不可恢复：回滚前必须完成账号 / 会话 / 审计数据导出（docs/identity-authn.md §17）。

DROP TABLE IF EXISTS identity_audit_event;
DROP TABLE IF EXISTS identity_session_family_archive;
DROP TABLE IF EXISTS identity_session_archive;
DROP TABLE IF EXISTS identity_oauth_identity;
DROP TABLE IF EXISTS identity_login_attempt;
DROP TABLE IF EXISTS identity_mfa;
DROP TABLE IF EXISTS identity_session_family;
DROP TABLE IF EXISTS identity_session;
DROP TABLE IF EXISTS identity_credential;
DROP TABLE IF EXISTS identity_account;
