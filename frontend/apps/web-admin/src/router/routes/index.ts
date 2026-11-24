import { createRoutes } from '@vben/app-kernel/router';

import { coreRoutes, fallbackNotFoundRoute } from './core';

const dynamicRouteFiles = import.meta.glob('./modules/**/*.ts', {
  eager: true,
});

const { accessRoutes, coreRouteNames, routes } = createRoutes({
  coreRoutes,
  dynamicModules: dynamicRouteFiles,
  fallbackNotFoundRoute,
});

export { accessRoutes, coreRouteNames, routes };
