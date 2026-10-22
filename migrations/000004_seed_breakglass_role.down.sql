-- 回滚破玻璃角色种子。
-- 仅当没有绑定引用时才能删除；存在历史绑定时由外键阻止（保留审计线索，需人工评估）。

DELETE FROM iam_role
WHERE id = 1900000000000000001
  AND channel = 'admin'
  AND role_type = 'breakglass';
