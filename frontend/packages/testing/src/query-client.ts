import { QueryClient } from '@tanstack/react-query';

/** 测试用 QueryClient：禁用重试与缓存，避免用例间串扰。 */
export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false },
    },
  });
}
