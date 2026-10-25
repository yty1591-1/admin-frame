import type { AdminAuthApi, MFASetupData, TokenPair } from '@admin-frame/api-client';
import { describe, expect, it, vi } from 'vitest';
import { SessionRuntime } from './session-runtime';
import { TokenStore, type SessionLike } from './token-store';

type AuthPort = Pick<
  AdminAuthApi,
  'login' | 'verifyMfaLogin' | 'verifyMfaSetup' | 'setupMfa' | 'logout' | 'logoutAll'
>;

function createPair(token: string): TokenPair {
  return {
    access_token: `access-${token}`,
    refresh_token: `refresh-${token}`,
    token_type: 'Bearer',
    expires_in: 600,
  };
}

const mfaLogin: AuthPort['login'] = () =>
  Promise.resolve({
    kind: 'mfa',
    challenge: { mfa_required: true, mfa_ticket: 'ticket-1' },
  });

const mfaSetupLogin: AuthPort['login'] = () =>
  Promise.resolve({
    kind: 'mfaSetup',
    challenge: { mfa_binding_required: true, mfa_setup_ticket: 'setup-1' },
  });

const setupData: MFASetupData = {
  secret: 'JBSWY3DPEHPK3PXP',
  otpauth_uri: 'otpauth://totp/admin-frame:alice?secret=JBSWY3DPEHPK3PXP',
  recovery_codes: ['recovery-1', 'recovery-2'],
};

function createFakeStorage(): SessionLike {
  const data = new Map<string, string>();
  return {
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => {
      data.set(key, value);
    },
    removeItem: (key) => {
      data.delete(key);
    },
  };
}

function createAuthPort(port: Partial<AuthPort> = {}): AuthPort {
  return {
    login: vi.fn(() => Promise.resolve({ kind: 'tokens', tokens: createPair('1') })),
    verifyMfaLogin: vi.fn(() => Promise.resolve(createPair('2'))),
    verifyMfaSetup: vi.fn(() => Promise.resolve(createPair('3'))),
    setupMfa: vi.fn(() => Promise.resolve(setupData)),
    logout: vi.fn(() => Promise.resolve()),
    logoutAll: vi.fn(() => Promise.resolve()),
    ...port,
  } as AuthPort;
}

function createRuntime(
  auth: AuthPort,
  options: { hasSession?: boolean; refresh?: () => Promise<string> } = {},
) {
  const store = new TokenStore({ namespace: 'test', storage: createFakeStorage() });
  if (options.hasSession) {
    store.setTokens(createPair('0'));
  }
  const refreshAccessToken = vi.fn(options.refresh ?? (() => Promise.resolve('access-restored')));
  const runtime = new SessionRuntime({
    auth,
    store,
    refreshManager: { refreshAccessToken },
  });
  return { runtime, store, refreshAccessToken };
}

describe('SessionRuntime', () => {
  it('密码登录成功写入令牌并进入已认证状态', async () => {
    const auth = createAuthPort();
    const { runtime, store } = createRuntime(auth);

    const result = await runtime.login('alice@example.com', 'secret');

    expect(result.kind).toBe('authenticated');
    expect(store.getAccessToken()).toBe('access-1');
    expect(runtime.hasMfaChallenge()).toBe(false);
  });

  it('MFA 挑战后用票据换取令牌并清空票据', async () => {
    const auth = createAuthPort({ login: vi.fn(mfaLogin) });
    const { runtime, store } = createRuntime(auth);

    const result = await runtime.login('alice@example.com', 'secret');

    expect(result).toEqual({ kind: 'mfa', ticket: 'ticket-1' });
    expect(runtime.hasMfaChallenge()).toBe(true);

    await runtime.verifyMfa('123456');

    expect(auth.verifyMfaLogin).toHaveBeenCalledWith('ticket-1', '123456');
    expect(store.getAccessToken()).toBe('access-2');
    expect(runtime.hasMfaChallenge()).toBe(false);
  });

  it('取消 MFA 后丢弃票据', async () => {
    const { runtime } = createRuntime(createAuthPort({ login: vi.fn(mfaLogin) }));
    await runtime.login('alice@example.com', 'secret');

    runtime.cancelMfa();

    expect(runtime.hasMfaChallenge()).toBe(false);
  });

  it('无票据时拒绝校验，避免空票据请求', async () => {
    const { runtime } = createRuntime(createAuthPort());

    await expect(runtime.verifyMfa('123456')).rejects.toMatchObject({
      kind: 'validation',
      code: 40000,
    });
  });

  it('动态码错误同样消费票据', async () => {
    const auth = createAuthPort({
      login: vi.fn(mfaLogin),
      verifyMfaLogin: vi.fn(() => Promise.reject(new Error('invalid code'))),
    });
    const { runtime } = createRuntime(auth);
    await runtime.login('alice@example.com', 'secret');

    await expect(runtime.verifyMfa('000000')).rejects.toThrowError('invalid code');
    expect(runtime.hasMfaChallenge()).toBe(false);
  });

  it('强制绑定：初始化后提交首码换取令牌并清空票据', async () => {
    const auth = createAuthPort({ login: vi.fn(mfaSetupLogin) });
    const { runtime, store } = createRuntime(auth);

    const result = await runtime.login('alice@example.com', 'secret');

    expect(result).toEqual({ kind: 'mfaSetup', ticket: 'setup-1' });
    expect(runtime.hasMfaSetupChallenge()).toBe(true);
    expect(runtime.hasMfaChallenge()).toBe(false);

    await expect(runtime.beginMfaSetup()).resolves.toEqual(setupData);
    expect(auth.setupMfa).toHaveBeenCalledWith('setup-1');

    await runtime.confirmMfaSetup('123456');

    expect(auth.verifyMfaSetup).toHaveBeenCalledWith('setup-1', '123456');
    expect(store.getAccessToken()).toBe('access-3');
    expect(runtime.hasMfaSetupChallenge()).toBe(false);
  });

  it('强制绑定失败后票据作废', async () => {
    const auth = createAuthPort({
      login: vi.fn(mfaSetupLogin),
      verifyMfaSetup: vi.fn(() => Promise.reject(new Error('invalid code'))),
    });
    const { runtime } = createRuntime(auth);
    await runtime.login('alice@example.com', 'secret');

    await expect(runtime.confirmMfaSetup('000000')).rejects.toThrowError('invalid code');
    expect(runtime.hasMfaSetupChallenge()).toBe(false);
  });

  it('取消强制绑定后丢弃票据', async () => {
    const { runtime } = createRuntime(createAuthPort({ login: vi.fn(mfaSetupLogin) }));
    await runtime.login('alice@example.com', 'secret');

    runtime.cancelMfaSetup();

    expect(runtime.hasMfaSetupChallenge()).toBe(false);
  });

  it('无绑定票据时拒绝初始化与提交', async () => {
    const { runtime } = createRuntime(createAuthPort());

    await expect(runtime.beginMfaSetup()).rejects.toMatchObject({
      kind: 'validation',
      code: 40000,
    });
    await expect(runtime.confirmMfaSetup('123456')).rejects.toMatchObject({
      kind: 'validation',
      code: 40000,
    });
  });

  it('无本地会话时恢复直接返回 false', async () => {
    const { runtime, refreshAccessToken } = createRuntime(createAuthPort());

    await expect(runtime.restore()).resolves.toBe(false);
    expect(refreshAccessToken).not.toHaveBeenCalled();
  });

  it('并发恢复只刷新一次（避免重复轮换）', async () => {
    const { runtime, refreshAccessToken } = createRuntime(createAuthPort(), { hasSession: true });

    const results = await Promise.all([runtime.restore(), runtime.restore()]);

    expect(results).toEqual([true, true]);
    expect(refreshAccessToken).toHaveBeenCalledTimes(1);
  });

  it('刷新失败时恢复为未认证', async () => {
    const { runtime } = createRuntime(createAuthPort(), {
      hasSession: true,
      refresh: () => Promise.reject(new Error('replay detected')),
    });

    await expect(runtime.restore()).resolves.toBe(false);
  });

  it('登出调用接口并清理本地令牌', async () => {
    const auth = createAuthPort();
    const { runtime, store } = createRuntime(auth, { hasSession: true });

    await runtime.logout();

    expect(auth.logout).toHaveBeenCalledTimes(1);
    expect(store.hasSession()).toBe(false);
  });

  it('登出接口失败时仍清理本地令牌', async () => {
    const auth = createAuthPort({ logout: vi.fn(() => Promise.reject(new Error('offline'))) });
    const { runtime, store } = createRuntime(auth, { hasSession: true });

    await expect(runtime.logout()).rejects.toThrowError('offline');
    expect(store.hasSession()).toBe(false);
  });

  it('登出全部会话递增版本并清空本地状态', async () => {
    const auth = createAuthPort();
    const { runtime, store } = createRuntime(auth, { hasSession: true });

    await runtime.logoutAll();

    expect(auth.logoutAll).toHaveBeenCalledTimes(1);
    expect(store.hasSession()).toBe(false);
  });
});
