import { AppError, type TokenPair } from '@admin-frame/api-client';
import type { TokenStore } from './token-store';

export type RefreshFn = (refreshToken: string) => Promise<TokenPair>;

export type RefreshManagerOptions = {
  /** 跨 Tab 互斥锁名，对应端别命名空间。 */
  namespace: string;
  store: TokenStore;
  refresh: RefreshFn;
  onSessionLost?: (() => void) | undefined;
};

/** 后端刷新令牌一次性轮换：并发 401 必须收敛为一次刷新（§10.3）。 */
export class RefreshManager {
  private inFlight: Promise<TokenPair> | null = null;
  private readonly lockName: string;
  private readonly store: TokenStore;
  private readonly refresh: RefreshFn;
  private readonly onSessionLost: (() => void) | undefined;

  constructor(options: RefreshManagerOptions) {
    this.lockName = `${options.namespace}:refresh`;
    this.store = options.store;
    this.refresh = options.refresh;
    this.onSessionLost = options.onSessionLost;
  }

  async refreshAccessToken(): Promise<string> {
    if (this.inFlight) {
      const pending = await this.inFlight;
      return pending.access_token;
    }
    const current = this.runExclusive();
    this.inFlight = current;
    try {
      const pair = await current;
      return pair.access_token;
    } finally {
      if (this.inFlight === current) {
        this.inFlight = null;
      }
    }
  }

  private async runExclusive(): Promise<TokenPair> {
    const run = async (): Promise<TokenPair> => {
      const refreshToken = this.store.getRefreshToken();
      if (!refreshToken) {
        this.loseSession();
        throw new AppError({ kind: 'unauthorized', code: 40100, message: '会话已失效' });
      }
      try {
        const pair = await this.refresh(refreshToken);
        this.store.setTokens(pair);
        return pair;
      } catch (error) {
        this.loseSession();
        throw error;
      }
    };

    const locks = typeof navigator === 'undefined' ? undefined : navigator.locks;
    if (!locks) {
      return run();
    }
    return await locks.request<Promise<TokenPair>>(this.lockName, () => run());
  }

  private loseSession(): void {
    this.store.clear();
    this.onSessionLost?.();
  }
}
