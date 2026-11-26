import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useAuthStore } from '../auth';

const mocks = vi.hoisted(() => {
  const accessStore = {
    accessToken: '' as string,
    loginExpired: false,
    setAccessCodes: vi.fn(),
    setAccessToken: vi.fn(),
    setLoginExpired: vi.fn(),
    setRefreshToken: vi.fn(),
  };
  const userStore = { setUserInfo: vi.fn() };
  const router = {
    currentRoute: {
      value: {
        fullPath: '/dashboard',
        path: '/dashboard',
        query: {} as Record<string, unknown>,
      },
    },
    push: vi.fn(),
    replace: vi.fn(),
  };
  return {
    accessStore,
    api: {
      completePasswordChangeApi: vi.fn(),
      confirmMfaBindingSessionApi: vi.fn(),
      disableMfaApi: vi.fn(),
      getMyPermissionsApi: vi.fn(),
      getUserInfoApi: vi.fn(),
      loginApi: vi.fn(),
      logoutApi: vi.fn(),
      mfaSetupApi: vi.fn(),
      mfaSetupSessionApi: vi.fn(),
      mfaVerifyApi: vi.fn(),
    },
    notificationSuccess: vi.fn(),
    resetAllStores: vi.fn(),
    router,
    t: vi.fn((key: string) => key),
    userStore,
  };
});

vi.mock('@vben/constants', () => ({ LOGIN_PATH: '/auth/login' }));
vi.mock('@vben/preferences', () => ({
  preferences: { app: { defaultHomePath: '/workbench' } },
}));
vi.mock('@vben/stores', () => ({
  resetAllStores: mocks.resetAllStores,
  useAccessStore: () => mocks.accessStore,
  useUserStore: () => mocks.userStore,
}));
vi.mock('vue-router', () => ({ useRouter: () => mocks.router }));
vi.mock('ant-design-vue', () => ({
  notification: { success: mocks.notificationSuccess },
}));
vi.mock('#/locales', () => ({ $t: mocks.t }));
vi.mock('#/api', () => mocks.api);

const tokens = { accessToken: 'biz-access', refreshToken: 'biz-refresh' };

describe('auth store（B 端）', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mocks.accessStore.accessToken = '';
    mocks.accessStore.loginExpired = false;
    mocks.router.currentRoute.value = {
      fullPath: '/dashboard',
      path: '/dashboard',
      query: {},
    };
    mocks.api.getUserInfoApi.mockResolvedValue({
      homePath: '/home',
      realName: '李四',
      userId: 2,
    });
    mocks.api.getMyPermissionsApi.mockResolvedValue({
      permissions: ['biz:order:read'],
      roles: ['operator'],
    });
    mocks.api.logoutApi.mockResolvedValue(undefined);
  });

  it('登录直接拿令牌：落库、合并角色、跳转默认首页并提示', async () => {
    mocks.api.loginApi.mockResolvedValue({ kind: 'token', tokens });

    const store = useAuthStore();
    await store.authLogin({ password: 'p', username: 'u' });

    expect(mocks.accessStore.setAccessToken).toHaveBeenCalledWith('biz-access');
    expect(mocks.accessStore.setRefreshToken).toHaveBeenCalledWith(
      'biz-refresh',
    );
    expect(mocks.userStore.setUserInfo).toHaveBeenCalledWith(
      expect.objectContaining({ realName: '李四', roles: ['operator'] }),
    );
    expect(mocks.accessStore.setAccessCodes).toHaveBeenCalledWith([
      'biz:order:read',
    ]);
    expect(mocks.router.push).toHaveBeenCalledWith('/workbench');
    expect(mocks.notificationSuccess).toHaveBeenCalledWith(
      expect.objectContaining({
        description: 'authentication.loginSuccessDesc:李四',
        message: 'authentication.loginSuccess',
      }),
    );
  });

  it('登录后优先回到 redirect 指定页面', async () => {
    mocks.router.currentRoute.value.query = {
      redirect: encodeURIComponent('/orders?page=1'),
    };
    mocks.api.loginApi.mockResolvedValue({ kind: 'token', tokens });

    await useAuthStore().authLogin({ password: 'p', username: 'u' });

    expect(mocks.router.push).toHaveBeenCalledWith('/orders?page=1');
  });

  it('登出失败也清理本地状态并回登录页', async () => {
    mocks.accessStore.accessToken = 'token-b';
    mocks.api.logoutApi.mockRejectedValue(new Error('network down'));

    const store = useAuthStore();
    await store.logout();

    expect(mocks.api.logoutApi).toHaveBeenCalledWith('token-b');
    expect(mocks.resetAllStores).toHaveBeenCalled();
    expect(mocks.accessStore.setLoginExpired).toHaveBeenCalledWith(false);
    expect(mocks.router.replace).toHaveBeenCalledWith({
      path: '/auth/login',
      query: { redirect: encodeURIComponent('/dashboard') },
    });
  });

  it('已在登录页时登出不再叠加 redirect', async () => {
    mocks.router.currentRoute.value = {
      fullPath: '/auth/login',
      path: '/auth/login',
      query: {},
    };

    await useAuthStore().logout();

    expect(mocks.router.replace).toHaveBeenCalledWith({
      path: '/auth/login',
      query: {},
    });
  });

  it('logout(false) 不带 redirect', async () => {
    await useAuthStore().logout(false);

    expect(mocks.router.replace).toHaveBeenCalledWith({
      path: '/auth/login',
      query: {},
    });
  });

  it('fetchUserInfo 并行拉资料与权限并合并角色', async () => {
    const merged = await useAuthStore().fetchUserInfo();

    expect(mocks.api.getUserInfoApi).toHaveBeenCalledTimes(1);
    expect(mocks.api.getMyPermissionsApi).toHaveBeenCalledTimes(1);
    expect(merged).toEqual({
      homePath: '/home',
      realName: '李四',
      roles: ['operator'],
      userId: 2,
    });
    expect(mocks.accessStore.setAccessCodes).toHaveBeenCalledWith([
      'biz:order:read',
    ]);
  });

  it('登录过程中 loginLoading 为 true', async () => {
    let resolveLogin: (value: unknown) => void = () => {};
    mocks.api.loginApi.mockReturnValue(
      new Promise((resolve) => {
        resolveLogin = resolve;
      }),
    );

    const store = useAuthStore();
    const pending = store.authLogin({ password: 'p', username: 'u' });
    expect(store.loginLoading).toBe(true);

    resolveLogin(tokens);
    await pending;
    expect(store.loginLoading).toBe(false);
  });
});
