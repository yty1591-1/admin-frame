import { describe, expect, it, vi } from 'vitest';

import { generateAccess } from '../access';

const mocks = vi.hoisted(() => {
  const createAccessGenerator = vi.fn(
    (_options: unknown) => async (payload: unknown) => payload,
  );
  const getAllMenusApi = vi.fn(async () => [{ name: 'dashboard' }]);
  return { createAccessGenerator, getAllMenusApi };
});

vi.mock('@vben/app-kernel/router', () => ({
  createAccessGenerator: mocks.createAccessGenerator,
}));

vi.mock('#/api', () => ({ getAllMenusApi: mocks.getAllMenusApi }));

vi.mock('#/layouts', () => ({
  BasicLayout: { name: 'BasicLayout' },
  IFrameView: { name: 'IFrameView' },
}));

describe('generateAccess wiring', () => {
  it('把菜单拉取器、布局映射与页面映射注入内核生成器', () => {
    expect(typeof generateAccess).toBe('function');

    const [options] = mocks.createAccessGenerator.mock.calls[0] as [
      {
        fetchMenuList: unknown;
        forbiddenComponent: unknown;
        layoutMap: Record<string, unknown>;
        pageMap: Record<string, unknown>;
      },
    ];

    expect(options.fetchMenuList).toBe(mocks.getAllMenusApi);
    expect(options.layoutMap).toEqual({
      BasicLayout: { name: 'BasicLayout' },
      IFrameView: { name: 'IFrameView' },
    });
    expect(options.forbiddenComponent).toBeTypeOf('function');
  });

  it('页面映射包含应用 views 下的 .vue 文件', () => {
    const [options] = mocks.createAccessGenerator.mock.calls[0] as [
      { pageMap: Record<string, unknown> },
    ];
    const pageKeys = Object.keys(options.pageMap);
    expect(pageKeys.length).toBeGreaterThan(0);
    for (const key of pageKeys) {
      expect(key.startsWith('../views/')).toBe(true);
      expect(key.endsWith('.vue')).toBe(true);
    }
  });
});
