import { createAdminClient, type AdminApiClient } from '@admin-frame/api-client';
import {
  RefreshManager,
  SessionRuntime,
  TokenStore,
  type TokenStorageMode,
} from '@admin-frame/auth';
import type { AppRuntimeConfig } from '@admin-frame/shared';

/** A/B 存储命名空间隔离（§6）。 */
export const ADMIN_SESSION_NAMESPACE = 'admin-frame:admin';

export type AuthBootstrap = {
  client: AdminApiClient;
  store: TokenStore;
  runtime: SessionRuntime;
};

export type AuthBootstrapOptions = {
  /**
   * 令牌存储模式（§10.2）：默认 `session`（过渡方案）；
   * `memory` 表示刷新页面即重新登录，测试用它隔离会话存储。
   */
  tokenStorageMode?: TokenStorageMode;
};

/**
 * 认证装配：令牌存储、单飞刷新与 HTTP 管线的唯一接线点（§9.2、§10.3）。
 * 业务代码不得自行创建 TokenStore 或 HttpClient。
 */
export function createAuthBootstrap(
  config: AppRuntimeConfig,
  onSessionLost: () => void,
  onForbidden?: (path: string) => void,
  options: AuthBootstrapOptions = {},
): AuthBootstrap {
  const store = new TokenStore({
    namespace: ADMIN_SESSION_NAMESPACE,
    mode: options.tokenStorageMode ?? 'session',
  });
  let refreshManager: RefreshManager | null = null;
  const client = createAdminClient({
    baseUrl: config.apiBaseUrl,
    getAccessToken: () => store.getAccessToken(),
    onForbidden,
    refreshAccessToken: async () => {
      if (!refreshManager) {
        return null;
      }
      return await refreshManager.refreshAccessToken();
    },
  });
  refreshManager = new RefreshManager({
    namespace: ADMIN_SESSION_NAMESPACE,
    store,
    refresh: (refreshToken) => client.auth.refresh(refreshToken),
    onSessionLost,
  });
  const runtime = new SessionRuntime({
    auth: client.auth,
    store,
    refreshManager,
  });
  return { client, store, runtime };
}
