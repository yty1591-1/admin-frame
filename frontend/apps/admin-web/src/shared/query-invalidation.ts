import type { QueryClient, QueryKey } from '@tanstack/react-query';

/**
 * 写操作后的精确失效模板（§8.2）：只失效声明的列表/详情/权限快照，
 * 禁止在页面里直接调用 `queryClient.clear()` 或全局 invalidate。
 */
export async function invalidateAfterWrite(
  queryClient: QueryClient,
  targets: readonly QueryKey[],
): Promise<void> {
  await Promise.all(targets.map((queryKey) => queryClient.invalidateQueries({ queryKey })));
}
