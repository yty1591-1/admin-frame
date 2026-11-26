-- 000040 (down): 将「配置与发布」挂回系统管理下。

SET @now = UTC_TIMESTAMP(3);
SET @actor = 'migration-promote-config-catalog-down';

UPDATE iam_menu
SET parent_code = 'system',
    route_path = '/system/config',
    redirect = '/system/config/items',
    sort = 40,
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config';

UPDATE iam_menu
SET route_path = 'items',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.items';

UPDATE iam_menu
SET route_path = 'edit',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.edit';

UPDATE iam_menu
SET route_path = 'versions',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.versions';

UPDATE iam_menu
SET route_path = 'releases',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.releases';

UPDATE iam_menu
SET route_path = 'secrets',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.secrets';

UPDATE iam_menu
SET route_path = 'feature-flags',
    updated_by = @actor,
    updated_at = @now
WHERE channel = 'admin' AND code = 'system.config.featureflags';
