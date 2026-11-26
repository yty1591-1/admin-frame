-- 回滚：删除 M2M 机器身份表（先删密钥子表，再删客户端主表）。
--
-- 说明：两张表只承载机器身份与密钥摘要，不涉及既有身份表结构变更，
-- 因此 down 不收缩任何既有列；回滚后 client_id 可被重新创建。
DROP TABLE IF EXISTS identity_m2m_secret;
DROP TABLE IF EXISTS identity_m2m_client;
