import { Suspense, lazy, type ComponentType, type ReactNode } from 'react';
import { createBrowserRouter, type RouteObject } from 'react-router';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { PageLoading } from '@admin-frame/ui';
import { AppLayout } from './AppLayout';

const HomePage = lazy(async () => import('../pages/HomePage'));
const NotFoundPage = lazy(async () => import('../pages/NotFoundPage'));

function lazyPage(Page: ComponentType): ReactNode {
  return (
    <Suspense fallback={<PageLoading />}>
      <Page />
    </Suspense>
  );
}

/** 路由即权限/菜单/懒加载的单一描述源（§7.1）；P1 加入 meta 守卫。 */
export function createAppRouter(config: AppRuntimeConfig) {
  const routes: RouteObject[] = [
    {
      path: '/',
      element: <AppLayout config={config} />,
      children: [
        { index: true, element: lazyPage(HomePage) },
        { path: '*', element: lazyPage(NotFoundPage) },
      ],
    },
  ];
  return createBrowserRouter(routes);
}
