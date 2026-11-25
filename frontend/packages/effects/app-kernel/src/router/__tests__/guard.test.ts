import type { Router } from 'vue-router';

import { beforeEach, describe, expect, it, vi } from 'vitest';

import { createRouterGuard } from '../guard';

const mocks = vi.hoisted(() => {
  const accessStore = {
    accessToken: '' as string,
    isAccessChecked: false,
    setAccessMenus: vi.fn(),
    setAccessRoutes: vi.fn(),
    setIsAccessChecked: vi.fn(),
  };
  const userStore = {
    setUserInfo: vi.fn(),
    userInfo: null as null | { homePath?: string; roles?: string[] },
  };
  const authStore = { fetchUserInfo: vi.fn() };
  const accessRoutes = [{ path: '/dynamic' }];
  const coreRouteNames = ['Login', 'FallbackNotFound'];
  return {
    accessRoutes,
    accessStore,
    authStore,
    coreRouteNames,
    generateAccess: vi.fn(),
    startProgress: vi.fn(),
    stopProgress: vi.fn(),
    userStore,
  };
});

vi.mock('@vben/constants', () => ({ LOGIN_PATH: '/auth/login' }));
vi.mock('@vben/preferences', () => ({
  preferences: {
    app: { defaultHomePath: '/analytics' },
    transition: { progress: true },
  },
}));
vi.mock('@vben/stores', () => ({
  useAccessStore: () => mocks.accessStore,
  useUserStore: () => mocks.userStore,
}));
vi.mock('@vben/utils', () => ({
  resolveSafeRedirect: (value: unknown, fallback: string) => {
    try {
      const decoded = decodeURIComponent(String(value ?? ''));
      return decoded.startsWith('/') ? decoded : fallback;
    } catch {
      return fallback;
    }
  },
  startProgress: mocks.startProgress,
  stopProgress: mocks.stopProgress,
}));

type Guard = (to: any, from: any) => any;

function createFakeRouter() {
  const before: Guard[] = [];
  const after: Array<(to: any) => any> = [];
  const router = {
    beforeEach: vi.fn((fn: Guard) => {
      before.push(fn);
    }),
    afterEach: vi.fn((fn: (to: any) => any) => {
      after.push(fn);
    }),
    resolve: vi.fn((path: string) => {
      // 模拟未注册路径（如历史残留 /dashboard）落到全局 404。
      if (path === '/dashboard') {
        return {
          fullPath: path,
          matched: [{ name: 'FallbackNotFound' }],
          name: 'FallbackNotFound',
          path,
          query: {},
        };
      }
      return {
        fullPath: path,
        matched: [{ name: 'Page' }],
        name: 'Page',
        path,
        query: {},
      };
    }),
  } as unknown as Router;

  createRouterGuard(router, {
    accessRoutes: mocks.accessRoutes as never,
    coreRouteNames: mocks.coreRouteNames,
    generateAccess: mocks.generateAccess,
    getAuthStore: () => mocks.authStore,
  });

  const [common, access] = before;
  if (!common || !access) {
    throw new Error('router guards are not registered');
  }

  return { access, after, common, router };
}

function route(overrides: Record<string, any> = {}) {
  return {
    fullPath: '/users',
    meta: {} as Record<string, any>,
    name: 'Users',
    path: '/users',
    query: {},
    ...overrides,
  };
}

describe('router guard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.accessStore.accessToken = '';
    mocks.accessStore.isAccessChecked = false;
    mocks.userStore.userInfo = null;
    mocks.authStore.fetchUserInfo.mockReset();
    mocks.generateAccess.mockReset();
  });

  it('通用守卫记录加载状态并启动进度条', async () => {
    const { common } = createFakeRouter();
    const to = route();

    const result = await common(to, {});

    expect(result).toBe(true);
    expect(to.meta.loaded).toBe(false);
    expect(mocks.startProgress).toHaveBeenCalledTimes(1);
  });

  it('已加载过的路径不再启动进度条', async () => {
    const { after, common } = createFakeRouter();
    const to = route();

    await common(to, {});
    await after[0]?.(to);
    mocks.startProgress.mockClear();
    await common(to, {});

    expect(to.meta.loaded).toBe(true);
    expect(mocks.startProgress).not.toHaveBeenCalled();
  });

  it('afterEach 记录路径并停止进度条', async () => {
    const { after } = createFakeRouter();
    const to = route();

    await after[0]?.(to);

    expect(mocks.stopProgress).toHaveBeenCalledTimes(1);
  });

  it('登录页且已持有令牌时跳回 redirect', async () => {
    const { access } = createFakeRouter();
    mocks.accessStore.accessToken = 'token';

    const result = await access(
      route({
        name: 'Login',
        path: '/auth/login',
        query: { redirect: encodeURIComponent('/users?x=1') },
      }),
      {},
    );

    expect(result).toBe('/users?x=1');
  });

  it('核心路由无需权限直接放行', async () => {
    const { access } = createFakeRouter();

    const result = await access(route({ name: 'FallbackNotFound' }), {});

    expect(result).toBe(true);
  });

  it('无令牌时跳转登录页并带 encodeURIComponent 后的 redirect', async () => {
    const { access } = createFakeRouter();

    const result = await access(route({ fullPath: '/users?page=2' }), {});

    expect(result).toEqual({
      path: '/auth/login',
      query: { redirect: encodeURIComponent('/users?page=2') },
      replace: true,
    });
  });

  it('无令牌且目标是首页时不带 redirect', async () => {
    const { access } = createFakeRouter();

    const result = await access(route({ fullPath: '/analytics' }), {});

    expect(result).toEqual({
      path: '/auth/login',
      query: {},
      replace: true,
    });
  });

  it('显式 ignoreAccess 的路由放行', async () => {
    const { access } = createFakeRouter();

    const result = await access(route({ meta: { ignoreAccess: true } }), {});

    expect(result).toBe(true);
  });

  it('已生成过动态路由时直接放行', async () => {
    const { access } = createFakeRouter();
    mocks.accessStore.accessToken = 'token';
    mocks.accessStore.isAccessChecked = true;

    const result = await access(route(), {});

    expect(result).toBe(true);
    expect(mocks.generateAccess).not.toHaveBeenCalled();
  });

  it('生成路由后写入 stores 并重定向到目标路径', async () => {
    const { access, router } = createFakeRouter();
    mocks.accessStore.accessToken = 'token';
    mocks.authStore.fetchUserInfo.mockResolvedValue({
      homePath: '/home',
      roles: ['ops'],
    });
    mocks.generateAccess.mockResolvedValue({
      accessibleMenus: [{ name: 'users' }],
      accessibleRoutes: [{ path: '/users' }],
    });

    const result = await access(route({ fullPath: '/users' }), { query: {} });

    expect(mocks.authStore.fetchUserInfo).toHaveBeenCalledTimes(1);
    expect(mocks.generateAccess).toHaveBeenCalledWith({
      roles: ['ops'],
      router,
      routes: [{ path: '/dynamic' }],
    });
    expect(mocks.accessStore.setAccessMenus).toHaveBeenCalledWith([
      { name: 'users' },
    ]);
    expect(mocks.accessStore.setAccessRoutes).toHaveBeenCalledWith([
      { path: '/users' },
    ]);
    expect(mocks.accessStore.setIsAccessChecked).toHaveBeenCalledWith(true);
    expect(router.resolve).toHaveBeenCalledWith('/users');
    expect(result).toEqual({
      fullPath: '/users',
      matched: [{ name: 'Page' }],
      name: 'Page',
      path: '/users',
      query: {},
      replace: true,
    });
  });

  it('已有用户资料时优先从 from.query.redirect 回跳', async () => {
    const { access, router } = createFakeRouter();
    mocks.accessStore.accessToken = 'token';
    mocks.userStore.userInfo = { homePath: '/home', roles: ['admin'] };
    mocks.generateAccess.mockResolvedValue({
      accessibleMenus: [],
      accessibleRoutes: [],
    });

    await access(route({ fullPath: '/users' }), {
      query: { redirect: encodeURIComponent('/from-redirect') },
    });

    expect(mocks.authStore.fetchUserInfo).not.toHaveBeenCalled();
    expect(router.resolve).toHaveBeenCalledWith('/from-redirect');
  });

  it('回跳目标未注册时回落到 homePath，避免登录后立刻 404', async () => {
    const { access, router } = createFakeRouter();
    mocks.accessStore.accessToken = 'token';
    mocks.userStore.userInfo = { homePath: '/home', roles: ['admin'] };
    mocks.generateAccess.mockResolvedValue({
      accessibleMenus: [],
      accessibleRoutes: [{ path: '/system', children: [{ path: 'role' }] }],
    });

    const result = await access(route({ fullPath: '/dashboard' }), {
      query: { redirect: encodeURIComponent('/dashboard') },
    });

    expect(router.resolve).toHaveBeenCalledWith('/dashboard');
    expect(router.resolve).toHaveBeenCalledWith('/home');
    expect(result).toEqual({
      fullPath: '/home',
      matched: [{ name: 'Page' }],
      name: 'Page',
      path: '/home',
      query: {},
      replace: true,
    });
  });
});
