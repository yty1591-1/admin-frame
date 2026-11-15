-- 000032 (down): 回退配置中心菜单，恢复基线占位 system.config（disabled menu）。

DELETE FROM iam_menu
WHERE channel = 'admin'
  AND code IN (
      'system.config.items',
      'system.config.edit',
      'system.config.versions',
      'system.config.releases'
  );

UPDATE iam_menu
SET node_type = 'menu',
    route_name = 'SystemConfig',
    route_path = 'config',
    component = '/system/config/index.vue',
    redirect = '',
    title = '系统配置',
    title_key = 'page.system.config',
    icon = 'lucide:sliders-horizontal',
    permission_code = 'config:read',
    sort = 40,
    hide_in_menu = 0,
    status = 'disabled',
    updated_by = 'migration-configcenter-menus-down',
    updated_at = UTC_TIMESTAMP(3)
WHERE channel = 'admin' AND code = 'system.config';
