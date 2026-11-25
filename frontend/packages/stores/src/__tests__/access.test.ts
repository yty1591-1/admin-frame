import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it } from 'vitest';

import { ACCESS_STORE_PERSIST_KEYS, useAccessStore } from '../modules/access';

describe('access store：锁屏口令不落盘（FE-SEC-05）', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('persist.pick 不含 lockScreenPassword', () => {
    const keys: readonly string[] = ACCESS_STORE_PERSIST_KEYS;
    expect(keys).not.toContain('lockScreenPassword');
  });

  it('持久化键固定为令牌 + 权限码 + 锁屏状态', () => {
    expect([...ACCESS_STORE_PERSIST_KEYS]).toEqual([
      'accessToken',
      'refreshToken',
      'accessCodes',
      'isLockScreen',
    ]);
  });

  it('lockScreen 仅内存保存口令，unlockScreen 立即清除', () => {
    const store = useAccessStore();
    store.lockScreen('secret');
    expect(store.isLockScreen).toBe(true);
    expect(store.lockScreenPassword).toBe('secret');

    store.unlockScreen();
    expect(store.isLockScreen).toBe(false);
    expect(store.lockScreenPassword).toBeUndefined();
  });
});
