import { createAdminClient, type AdminApiClient } from '@admin-frame/api-client';
import { RefreshManager, SessionRuntime, TokenStore } from '@admin-frame/auth';
import type { AppRuntimeConfig } from '@admin-frame/shared';

/** A/B 存储命名空间隔离（§6）。 */
export const ADMIN_SESSION_NAMESPACE = 'admin-frame:admin';

export type AuthBootstrap = {
  client: AdminApiClient;
  store: TokenStore;
  runtime: SessionRuntime;
};

/**
 * 认证装配：令牌存储、单飞刷新与 HTTP 管线的唯一接线点（§9.2、§10.3）。
 * 业务代码不得自行创建 TokenStore 或 HttpClient。
 */
export function createAuthBootstrap(
  config: AppRuntimeConfig,
  onSessionLost: () => void,
  onForbidden?: (path: string) => void,
): AuthBootstrap {
  const store = new TokenStore({ namespace: ADMIN_SESSION_NAMESPACE });
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
