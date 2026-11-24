import { createAppRequestClient } from '@vben/app-kernel/api/request';
import { useAppConfig } from '@vben/hooks';

import { useAuthStore } from '#/store';

import { refreshTokenApi } from './core';

const { apiURL } = useAppConfig(import.meta.env, import.meta.env.PROD);

/**
 * 应用请求客户端（鉴权/刷新/错误呈现在内核；B 端不注入幂等头）。
 */
const { baseRequestClient, requestClient } = createAppRequestClient({
  apiURL,
  getAuthStore: () => useAuthStore(),
  refreshTokenApi,
});

export { baseRequestClient, requestClient };
