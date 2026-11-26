import { createAppRouter } from '@vben/app-kernel/router';

import { useAuthStore } from '#/store';

import { generateAccess } from './access';
import { accessRoutes, coreRouteNames, routes } from './routes';

/**
 * 创建 vue-router 实例（守卫与组装逻辑在内核，应用只注入依赖）。
 */
const { resetRoutes, router } = createAppRouter({
  accessRoutes,
  coreRouteNames,
  generateAccess,
  getAuthStore: () => useAuthStore(),
  routes,
});

export { resetRoutes, router };
