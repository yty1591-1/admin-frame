-- 000040 (up): 将「配置与发布」提升为顶级目录，对齐画布 IA。
--
-- 背景：000039 仅改标题/排序，未把 system.config 从 system 下解挂。
-- 影响：侧栏「配置与发布」与「系统管理」并列；路由 path 保持 /system/config/*。

SET @now = UTC_TIMESTAMP(3);
SET @actor = 'migration-promote-config-catalog';

UPDATE iam_menu
SET parent_code = NULL,
    route_path = '/system/config',
    redirect = '/system/config/items',
    title = '配置与发布',
    title_key = 'page.config.title',
    icon = 'lucide:sliders-horizontal',
    sort = 40,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config';

-- 叶子改为绝对路径，避免父节点升为顶级后相对拼接异常
UPDATE iam_menu
SET route_path = '/system/config/items',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.items';

UPDATE iam_menu
SET route_path = '/system/config/edit',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.edit';

UPDATE iam_menu
SET route_path = '/system/config/versions',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.versions';

UPDATE iam_menu
SET route_path = '/system/config/releases',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.releases';

UPDATE iam_menu
SET route_path = '/system/config/secrets',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.secrets';

UPDATE iam_menu
SET route_path = '/system/config/feature-flags',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.featureflags';
