import type { RouteRecordRaw } from 'vue-router';

import { mergeRouteModules, traverseTreeValues } from '@vben/utils';

export interface CreateRoutesOptions {
  coreRoutes: RouteRecordRaw[];
  dynamicModules: Record<string, unknown>;
  fallbackNotFoundRoute: RouteRecordRaw;
}

/**
 * 组装应用路由表：core 路由（不走权限）+ 动态路由（走权限）+ 404 兜底。
 *
 * 各应用只负责用 `import.meta.glob` 采集文件并把 core 路由注入进来，
 * 组装逻辑集中在内核，避免两端漂移。
 */
export function createRoutes({
  coreRoutes,
  dynamicModules,
  fallbackNotFoundRoute,
}: CreateRoutesOptions) {
  const dynamicRoutes: RouteRecordRaw[] = mergeRouteModules(dynamicModules);

  /** 路由列表，由基本路由与 404 兜底路由组成 */
  const routes: RouteRecordRaw[] = [...coreRoutes, fallbackNotFoundRoute];

  /** 基本路由列表，这些路由不需要进入权限拦截 */
  const coreRouteNames = traverseTreeValues(coreRoutes, (route) => route.name);

  /** 有权限校验的路由列表 */
  const accessRoutes = [...dynamicRoutes];

  return { accessRoutes, coreRouteNames, routes };
}
