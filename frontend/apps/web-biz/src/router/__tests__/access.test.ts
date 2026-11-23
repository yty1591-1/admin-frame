import type { GenerateMenuAndRoutesOptions } from '@vben/types';

import { beforeEach, describe, expect, it, vi } from 'vitest';

import { generateAccess } from '../access';

const mocks = vi.hoisted(() => ({
  generateAccessible: vi.fn(),
  getAllMenusApi: vi.fn(),
  loading: vi.fn(),
  t: vi.fn((key: string) => key),
}));

vi.mock('@vben/access', () => ({
  generateAccessible: mocks.generateAccessible,
}));

vi.mock('@vben/preferences', () => ({
  preferences: { app: { accessMode: 'backend' } },
}));

vi.mock('ant-design-vue', () => ({
  message: { loading: mocks.loading },
}));

vi.mock('#/api', () => ({
  getAllMenusApi: mocks.getAllMenusApi,
}));

vi.mock('#/layouts', () => ({
  BasicLayout: { name: 'BasicLayout' },
  IFrameView: { name: 'IFrameView' },
}));

vi.mock('#/locales', () => ({ $t: mocks.t }));

describe('generateAccess', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.generateAccessible.mockResolvedValue({
      accessibleMenus: [{ name: 'dashboard' }],
      accessibleRoutes: [{ path: '/dashboard' }],
    });
    mocks.getAllMenusApi.mockResolvedValue([{ name: 'dashboard' }]);
  });

  it('把页面映射、布局映射与菜单拉取器传给 generateAccessible', async () => {
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
    const [mode, calledOptions] = mocks.generateAccessible.mock.calls[0] as [
      string,
      GenerateMenuAndRoutesOptions & {
        layoutMap: Record<string, unknown>;
        pageMap: Record<string, unknown>;
      },
    ];
    expect(mode).toBe('backend');
    expect(calledOptions.roles).toEqual(['admin']);

    const pageKeys = Object.keys(calledOptions.pageMap);
    expect(pageKeys.length).toBeGreaterThan(0);
    for (const key of pageKeys) {
      expect(key.startsWith('../views/')).toBe(true);
      expect(key.endsWith('.vue')).toBe(true);
    }
    expect(calledOptions.layoutMap).toEqual({
      BasicLayout: { name: 'BasicLayout' },
      IFrameView: { name: 'IFrameView' },
    });
  });

  it('fetchMenuListAsync 触发 loading 并返回菜单数据', async () => {
    await generateAccess({
      roles: [],
      router: {} as never,
      routes: [],
    });

    const [, options] = mocks.generateAccessible.mock.calls[0] as [
      string,
      GenerateMenuAndRoutesOptions & {
        fetchMenuListAsync: () => Promise<unknown>;
      },
    ];
    const menus = await options.fetchMenuListAsync();

    expect(mocks.loading).toHaveBeenCalledWith({
      content: 'common.loadingMenu...',
      duration: 1.5,
    });
    expect(mocks.getAllMenusApi).toHaveBeenCalledTimes(1);
    expect(menus).toEqual([{ name: 'dashboard' }]);
  });
});
