-- 回滚 SCIM 2.0（docs/identity-authn.md §21.1.8）。
--
-- 顺序：先删成员（外键指向组与账号）→ 再删组 → 最后删令牌；
-- provider 收窄放最后，且存在 scim: 前缀数据时拒绝收窄，避免把映射截断成脏数据。

DROP TABLE IF EXISTS identity_scim_group_member;
DROP TABLE IF EXISTS identity_scim_group;
DROP TABLE IF EXISTS identity_scim_token;

-- 存在 SCIM 映射时收窄会截断 provider，必须先清理数据再回滚。
SET @scim_rows := (SELECT COUNT(*) FROM identity_oauth_identity WHERE provider LIKE 'scim:%');
SET @sql := IF(@scim_rows = 0,
    'ALTER TABLE identity_oauth_identity MODIFY COLUMN provider VARCHAR(32) NOT NULL COMMENT ''外部身份提供方标识，如 oidc-corp''',
    'SELECT ''identity_oauth_identity still contains scim mappings; clean them before narrowing provider''');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
