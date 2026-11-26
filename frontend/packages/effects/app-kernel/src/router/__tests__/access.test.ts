import type { GenerateMenuAndRoutesOptions } from '@vben/types';

import { beforeEach, describe, expect, it, vi } from 'vitest';

import { createAccessGenerator } from '../access';

const mocks = vi.hoisted(() => ({
  generateAccessible: vi.fn(),
  loading: vi.fn(),
  t: vi.fn((key: string) => key),
}));

vi.mock('@vben/access', () => ({
  generateAccessible: mocks.generateAccessible,
}));

vi.mock('@vben/preferences', () => ({
  preferences: { app: { accessMode: 'backend' } },
}));

vi.mock('@vben/locales', () => ({ $t: mocks.t }));

vi.mock('ant-design-vue', () => ({
  message: { loading: mocks.loading },
}));

const pageMap = { '../views/dashboard/index.vue': () => Promise.resolve({}) };
const layoutMap = { BasicLayout: () => {}, IFrameView: () => {} };

describe('createAccessGenerator', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.generateAccessible.mockResolvedValue({
      accessibleMenus: [{ name: 'dashboard' }],
      accessibleRoutes: [{ path: '/dashboard' }],
    });
  });

  it('把 accessMode、页面映射、布局映射传给 generateAccessible', async () => {
    const fetchMenuList = vi.fn(async () => [{ name: 'dashboard' }]);
    const generateAccess = createAccessGenerator({
      fetchMenuList,
      layoutMap: layoutMap as never,
      pageMap: pageMap as never,
    });
    const options = {
      roles: ['admin'],
      router: {} as never,
      routes: [],
    } satisfies GenerateMenuAndRoutesOptions;

    const result = await generateAccess(options);

    expect(result).toEqual({
      accessibleMenus: [{ name: 'dashboard' }],
      accessibleRoutes: [{ path: '/dashboard' }],
    });
    expect(mocks.generateAccessible).toHaveBeenCalledTimes(1);
    const [mode, called] = mocks.generateAccessible.mock.calls[0] as [
      string,
      GenerateMenuAndRoutesOptions & Record<string, unknown>,
    ];
    expect(mode).toBe('backend');
    expect(called.layoutMap).toEqual(layoutMap);
    expect(called.pageMap).toEqual(pageMap);
    expect(called.roles).toEqual(['admin']);
  });

  it('fetchMenuListAsync 先展示 loading 再返回菜单', async () => {
    const fetchMenuList = vi.fn(async () => [{ name: 'dashboard' }]);
    const generateAccess = createAccessGenerator({
      fetchMenuList,
      layoutMap: layoutMap as never,
      pageMap: pageMap as never,
    });

    await generateAccess({ roles: [], router: {} as never, routes: [] });
    const [, called] = mocks.generateAccessible.mock.calls[0] as [
      string,
      { fetchMenuListAsync: () => Promise<unknown> },
    ];
    const menus = await called.fetchMenuListAsync();

    expect(mocks.loading).toHaveBeenCalledWith({
      content: 'common.loadingMenu...',
      duration: 1.5,
    });
    expect(fetchMenuList).toHaveBeenCalledTimes(1);
    expect(menus).toEqual([{ name: 'dashboard' }]);
  });
});
