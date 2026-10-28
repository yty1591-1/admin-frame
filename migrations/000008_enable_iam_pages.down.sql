-- 回滚 000008：把本次启用的页面菜单恢复为 disabled；iam.sod 保留记录但停用，
-- 不物理删除，避免破坏菜单审计与父子引用。

UPDATE iam_menu
SET status = 'disabled', updated_by = 'seed', updated_at = UTC_TIMESTAMP(3)
WHERE channel = 'admin'
  AND code IN (
    'system.menu',
    'iam.permission',
    'iam.binding',
    'iam.template',
    'iam.change',
    'iam.breakglass',
    'iam.check',
    'iam.sod'
  );
