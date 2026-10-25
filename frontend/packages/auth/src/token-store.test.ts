import { describe, expect, it } from 'vitest';
import type { TokenPair } from '@admin-frame/api-client';
import { TokenStore, type SessionLike } from './token-store';

const pair: TokenPair = {
  access_token: 'access-1',
  refresh_token: 'refresh-1',
  token_type: 'Bearer',
  expires_in: 600,
};

function createFakeStorage(): SessionLike & { readonly data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => {
      data.set(key, value);
    },
    removeItem: (key) => {
      data.delete(key);
    },
    data,
  };
}

describe('TokenStore', () => {
  it('session 模式持久化刷新令牌并保留命名空间', () => {
    const storage = createFakeStorage();
    const store = new TokenStore({ namespace: 'admin-frame:admin', storage });

    store.setTokens(pair);

    expect(store.getAccessToken()).toBe('access-1');
    expect(store.getRefreshToken()).toBe('refresh-1');
    expect(storage.data.get('admin-frame:admin:refresh')).toBe('refresh-1');
  });

  it('memory 模式不触达任何存储', () => {
    const storage = createFakeStorage();
    const store = new TokenStore({ namespace: 'admin-frame:admin', mode: 'memory', storage });

    store.setTokens(pair);

    expect(storage.data.size).toBe(0);
    expect(store.getRefreshToken()).toBeNull();
    expect(store.hasSession()).toBe(true);
  });

  it('clear 同时清理内存与存储', () => {
    const storage = createFakeStorage();
    const store = new TokenStore({ namespace: 'admin-frame:biz', storage });
    store.setTokens(pair);

    store.clear();

    expect(store.hasSession()).toBe(false);
    expect(storage.data.size).toBe(0);
  });
});
