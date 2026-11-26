-- 000041 (down): 收回 system_admin 的 iam:breakglass:approve。

SET @role_id := (SELECT id FROM iam_role
                 WHERE channel = 'admin' AND merchant_id = '' AND code = 'system_admin' LIMIT 1);

DELETE FROM iam_role_permission
WHERE role_id = @role_id
  AND permission_code = 'iam:breakglass:approve'
  AND @role_id IS NOT NULL;
