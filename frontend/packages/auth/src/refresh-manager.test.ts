import type { TokenPair } from '@admin-frame/api-client';
import { describe, expect, it, vi } from 'vitest';
import { RefreshManager } from './refresh-manager';
import { TokenStore, type SessionLike } from './token-store';

function createPair(token: string): TokenPair {
  return {
    access_token: `access-${token}`,
    refresh_token: `refresh-${token}`,
    token_type: 'Bearer',
    expires_in: 600,
  };
}

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

function createStore(): TokenStore {
  // 刷新令牌只在 session 模式持久化（§10.2），单飞刷新测试需可读回刷新令牌。
  return new TokenStore({ namespace: 'test', storage: createFakeStorage() });
}

describe('RefreshManager', () => {
  it('并发 401 只发起一次刷新（single-flight）', async () => {
    const store = createStore();
    store.setTokens(createPair('1'));
    const refresh = vi.fn(() => Promise.resolve(createPair('2')));
    const manager = new RefreshManager({ namespace: 'test', store, refresh });

    const tokens = await Promise.all([
      manager.refreshAccessToken(),
      manager.refreshAccessToken(),
      manager.refreshAccessToken(),
    ]);

    expect(refresh).toHaveBeenCalledTimes(1);
    expect(tokens).toEqual(['access-2', 'access-2', 'access-2']);
    expect(store.getAccessToken()).toBe('access-2');
  });

  it('无刷新令牌时清空会话并拒绝', async () => {
    const store = createStore();
    store.setTokens(createPair('1'));
    store.clear();
    const onSessionLost = vi.fn();
    const manager = new RefreshManager({
      namespace: 'test',
      store,
      refresh: () => Promise.resolve(createPair('2')),
      onSessionLost,
    });

    await expect(manager.refreshAccessToken()).rejects.toMatchObject({
      kind: 'unauthorized',
      code: 40100,
    });
    expect(onSessionLost).toHaveBeenCalledTimes(1);
  });

  it('刷新失败时清理会话并向上抛出', async () => {
    const store = createStore();
    store.setTokens(createPair('1'));
    const onSessionLost = vi.fn();
    const manager = new RefreshManager({
      namespace: 'test',
      store,
      refresh: () => Promise.reject(new Error('replay detected')),
      onSessionLost,
    });

    await expect(manager.refreshAccessToken()).rejects.toThrowError('replay detected');
    expect(store.hasSession()).toBe(false);
    expect(onSessionLost).toHaveBeenCalledTimes(1);
  });
});
