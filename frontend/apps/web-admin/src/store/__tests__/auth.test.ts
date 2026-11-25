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
  preferences: { app: { defaultHomePath: '/analytics' } },
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

const tokens = { accessToken: 'access-1', refreshToken: 'refresh-1' };

function permissions() {
  return { permissions: ['iam:user:read'], roles: ['admin'] };
}

describe('auth store（A 端）', () => {
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
      realName: '张三',
      userId: 1,
    });
    mocks.api.getMyPermissionsApi.mockResolvedValue(permissions());
    mocks.api.logoutApi.mockResolvedValue(undefined);
  });

  it('token 态登录：落库双令牌、合并角色、跳转默认首页并提示', async () => {
    mocks.api.loginApi.mockResolvedValue({ kind: 'token', tokens });

    const store = useAuthStore();
    const outcome = await store.authLogin({
      password: 's3cret',
      username: 'u',
    });

    expect(outcome).toEqual({ kind: 'token', tokens });
    expect(mocks.accessStore.setAccessToken).toHaveBeenCalledWith('access-1');
    expect(mocks.accessStore.setRefreshToken).toHaveBeenCalledWith('refresh-1');
    expect(mocks.userStore.setUserInfo).toHaveBeenCalledWith(
      expect.objectContaining({ realName: '张三', roles: ['admin'] }),
    );
    expect(mocks.accessStore.setAccessCodes).toHaveBeenCalledWith([
      'iam:user:read',
    ]);
    expect(mocks.router.push).toHaveBeenCalledWith('/analytics');
    expect(mocks.notificationSuccess).toHaveBeenCalledWith(
      expect.objectContaining({
        description: 'authentication.loginSuccessDesc:张三',
        message: 'authentication.loginSuccess',
      }),
    );
    expect(store.loginLoading).toBe(false);
  });

  it('登录后优先回到 redirect 指定页面', async () => {
    mocks.router.currentRoute.value.query = {
      redirect: encodeURIComponent('/users?tab=1'),
    };
    mocks.api.loginApi.mockResolvedValue({ kind: 'token', tokens });

    await useAuthStore().authLogin({ password: 'p', username: 'u' });

    expect(mocks.router.push).toHaveBeenCalledWith('/users?tab=1');
  });

  it.each(['password_change', 'mfa_challenge', 'mfa_bind'])(
    '%s 中间态不写令牌也不跳转',
    async (kind) => {
      mocks.api.loginApi.mockResolvedValue({ kind });
      mocks.api.completePasswordChangeApi.mockResolvedValue({ kind });

      const store = useAuthStore();
      const outcome = await store.authLogin({ password: 'p', username: 'u' });

      expect(outcome.kind).toBe(kind);
      expect(mocks.accessStore.setAccessToken).not.toHaveBeenCalled();
      expect(mocks.router.push).not.toHaveBeenCalled();
      expect(store.loginLoading).toBe(false);
    },
  );

  it('改密成功后若返回令牌则走统一登录收尾', async () => {
    mocks.api.completePasswordChangeApi.mockResolvedValue({
      kind: 'token',
      tokens,
    });

    await useAuthStore().completePasswordChange('ticket', 'new-pass');

    expect(mocks.api.completePasswordChangeApi).toHaveBeenCalledWith({
      newPassword: 'new-pass',
      ticket: 'ticket',
    });
    expect(mocks.accessStore.setAccessToken).toHaveBeenCalledWith('access-1');
    expect(mocks.router.push).toHaveBeenCalledWith('/analytics');
  });

  it('mFA 校验通过后落库令牌', async () => {
    mocks.api.mfaVerifyApi.mockResolvedValue(tokens);

    await useAuthStore().verifyMfaChallenge('ticket', '123456');

    expect(mocks.api.mfaVerifyApi).toHaveBeenCalledWith({
      code: '123456',
      mfaTicket: 'ticket',
    });
    expect(mocks.accessStore.setAccessToken).toHaveBeenCalledWith('access-1');
    expect(mocks.router.push).toHaveBeenCalled();
  });

  it('强制绑定确认后落库令牌', async () => {
    mocks.api.mfaVerifyApi.mockResolvedValue(tokens);

    await useAuthStore().confirmMfaBinding('ticket', '654321');

    expect(mocks.api.mfaVerifyApi).toHaveBeenCalledWith({
      code: '654321',
      mfaSetupTicket: 'ticket',
    });
    expect(mocks.accessStore.setAccessToken).toHaveBeenCalled();
  });

  it('解绑 MFA 后统一登出', async () => {
    mocks.accessStore.accessToken = 'token-x';
    mocks.api.disableMfaApi.mockResolvedValue(undefined);

    await useAuthStore().disableMfaForAccount('000000');

    expect(mocks.api.disableMfaApi).toHaveBeenCalledWith('000000', 'token-x');
    expect(mocks.api.logoutApi).toHaveBeenCalledWith('token-x');
    expect(mocks.resetAllStores).toHaveBeenCalled();
    expect(mocks.router.replace).toHaveBeenCalledWith({
      path: '/auth/login',
      query: { redirect: encodeURIComponent('/dashboard') },
    });
  });

  it('登出失败也清理本地状态；已在登录页时不带 redirect', async () => {
    mocks.router.currentRoute.value = {
      fullPath: '/auth/login',
      path: '/auth/login',
      query: {},
    };
    mocks.api.logoutApi.mockRejectedValue(new Error('network down'));

    const store = useAuthStore();
    await store.logout();

    expect(mocks.resetAllStores).toHaveBeenCalled();
    expect(mocks.accessStore.setLoginExpired).toHaveBeenCalledWith(false);
    expect(mocks.router.replace).toHaveBeenCalledWith({
      path: '/auth/login',
      query: {},
    });
  });

  it('logout(false) 不带 redirect 参数', async () => {
    const store = useAuthStore();
    await store.logout(false);

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
      realName: '张三',
      roles: ['admin'],
      userId: 1,
    });
    expect(mocks.accessStore.setAccessCodes).toHaveBeenCalledWith([
      'iam:user:read',
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

    resolveLogin({ kind: 'password_change' });
    await pending;
    expect(store.loginLoading).toBe(false);
  });
});
