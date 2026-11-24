import { createAppRequestClient } from '@vben/app-kernel/api/request';
import { useAppConfig } from '@vben/hooks';

import { useAuthStore } from '#/store';

import { refreshTokenApi } from './core';
import { ensureIdempotencyHeader } from './idempotency';

const { apiURL } = useAppConfig(import.meta.env, import.meta.env.PROD);

/**
 * 应用请求客户端（鉴权/刷新/错误呈现在内核，幂等头按能力注入）。
 */
const { baseRequestClient, requestClient } = createAppRequestClient({
  apiURL,
  ensureIdempotencyHeader,
  getAuthStore: () => useAuthStore(),
  refreshTokenApi,
});

export { baseRequestClient, requestClient };
