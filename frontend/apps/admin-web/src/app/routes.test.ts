import { createPermissionChecker } from '@admin-frame/auth';
import { defaultTranslator } from '@admin-frame/i18n';
import { describe, expect, it } from 'vitest';
import { buildMenuEntries } from './routes';

const t = defaultTranslator;

describe('buildMenuEntries', () => {
  it('无业务权限时只保留工作台入口', () => {
    const menus = buildMenuEntries(createPermissionChecker([]), t);

    expect(menus).toEqual([{ key: '/', label: t('nav.home') }]);
  });

  it('按权限过滤并按 menuOrder 排序', () => {
    const menus = buildMenuEntries(
      createPermissionChecker(['iam:role:read', 'iam:permission:read']),
      t,
    );

    expect(menus.map((menu) => menu.key)).toEqual(['/', '/iam/permissions', '/iam/roles']);
  });

  it('只有角色读权限时不展示权限目录', () => {
    const menus = buildMenuEntries(createPermissionChecker(['iam:role:read']), t);

    expect(menus.map((menu) => menu.key)).toEqual(['/', '/iam/roles']);
  });
});
