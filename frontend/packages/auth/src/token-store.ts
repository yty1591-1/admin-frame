import type { TokenPair } from '@admin-frame/api-client';

/** `sessionStorage` 的最小接口；便于测试注入与后续切换 HttpOnly Cookie 适配器。 */
export type SessionLike = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

export type TokenStorageMode = 'memory' | 'session';

export type TokenStoreOptions = {
  /** 按端隔离的命名空间，如 admin-frame:admin（§6）。 */
  namespace: string;
  /** memory：刷新页面即重新登录的最高安全模式；session：过渡方案（§10.2）。 */
  mode?: TokenStorageMode;
  storage?: SessionLike | null;
};

const REFRESH_SUFFIX = ':refresh';

function defaultStorage(): SessionLike | null {
  if (typeof window === 'undefined') {
    return null;
  }
  return window.sessionStorage;
}

/**
 * 唯一令牌存储实现（FE-07）。
 * Access Token 只驻留内存；Refresh Token 仅在 session 模式下写入 sessionStorage，
 * 禁止 localStorage、URL、日志与 IndexedDB。
 */
export class TokenStore {
  private accessToken: string | null = null;
  private readonly refreshKey: string;
  private readonly storage: SessionLike | null;

  constructor(options: TokenStoreOptions) {
    this.refreshKey = `${options.namespace}${REFRESH_SUFFIX}`;
    this.storage =
      (options.mode ?? 'session') === 'session' ? (options.storage ?? defaultStorage()) : null;
  }

  setTokens(pair: TokenPair): void {
    this.accessToken = pair.access_token;
    this.storage?.setItem(this.refreshKey, pair.refresh_token);
  }

  getAccessToken(): string | null {
    return this.accessToken;
  }

  getRefreshToken(): string | null {
    return this.storage?.getItem(this.refreshKey) ?? null;
  }

  hasSession(): boolean {
    return this.accessToken !== null || this.getRefreshToken() !== null;
  }

  clear(): void {
    this.accessToken = null;
    this.storage?.removeItem(this.refreshKey);
  }
}
