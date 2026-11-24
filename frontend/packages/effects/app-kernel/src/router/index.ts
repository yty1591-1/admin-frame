import type { RouteRecordRaw } from 'vue-router';

import type { RouterGuardOptions } from './guard';

import {
  createRouter,
  createWebHashHistory,
  createWebHistory,
} from 'vue-router';

import { resetStaticRoutes } from '@vben/utils';

import { createRouterGuard } from './guard';

export interface CreateAppRouterOptions extends RouterGuardOptions {
  routes: RouteRecordRaw[];
}

/**
 * 创建 vue-router 实例并注册守卫。
 *
 * 应用侧只提供路由表、菜单生成器与 auth store 访问器。
 */
export function createAppRouter(options: CreateAppRouterOptions) {
  const { routes, ...guardOptions } = options;

  const router = createRouter({
    history:
      import.meta.env.VITE_ROUTER_HISTORY === 'hash'
        ? createWebHashHistory(import.meta.env.VITE_BASE)
        : createWebHistory(import.meta.env.VITE_BASE),
    routes,
    scrollBehavior: (to, _from, savedPosition) => {
      if (savedPosition) {
        return savedPosition;
      }
      return to.hash
        ? { behavior: 'smooth', el: to.hash }
        : { left: 0, top: 0 };
    },
  });

  const resetRoutes = () => resetStaticRoutes(router, routes);

  createRouterGuard(router, guardOptions);

  return { resetRoutes, router };
}

export { createAccessGenerator } from './access';
export type { AccessGeneratorOptions } from './access';
export type { RouterGuardOptions } from './guard';
export { createRoutes } from './routes';
export type { CreateRoutesOptions } from './routes';
