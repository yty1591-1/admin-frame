import { createAppRequestClient } from '@vben/app-kernel/api/request';
import { useAppConfig } from '@vben/hooks';

import { useAuthStore } from '#/store';

const { apiURL } = useAppConfig(import.meta.env, import.meta.env.PROD);

/**
 * 应用请求客户端（鉴权/刷新/错误呈现在内核；B 端不注入幂等头）。
 */
const { baseRequestClient, requestClient } = createAppRequestClient({
  apiURL,
  getAuthStore: () => useAuthStore(),
  // 延迟解析，避免 api/request 与 api/core/auth 的模块环（FE-T-04 发现：prd 产物登录 undefined.post）
  refreshTokenApi: (refreshToken: string) =>
    import('./core/auth').then((m) => m.refreshTokenApi(refreshToken)),
});

export { baseRequestClient, requestClient };
