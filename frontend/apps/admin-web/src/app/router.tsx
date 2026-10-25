import { Suspense, lazy, type ComponentType, type ReactNode } from 'react';
import { createBrowserRouter, type RouteObject } from 'react-router';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { PageLoading } from '@admin-frame/ui';
import { AppLayout } from './AppLayout';
import { RequireAuth } from './auth/RequireAuth';

const HomePage = lazy(async () => import('../pages/HomePage'));
const LoginPage = lazy(async () => import('../pages/LoginPage'));
const ForbiddenPage = lazy(async () => import('../pages/ForbiddenPage'));
const NotFoundPage = lazy(async () => import('../pages/NotFoundPage'));

function lazyPage(Page: ComponentType): ReactNode {
  return (
    <Suspense fallback={<PageLoading />}>
      <Page />
    </Suspense>
  );
}

/** 路由即权限/菜单/懒加载的单一描述源（§7.1）；权限 meta 在 P1 后续切片补齐。 */
export function createAppRouter(config: AppRuntimeConfig) {
  const routes: RouteObject[] = [
    {
      path: '/login',
      element: lazyPage(LoginPage),
    },
    {
      path: '/',
      element: (
        <RequireAuth>
          <AppLayout config={config} />
        </RequireAuth>
      ),
      children: [
        { index: true, element: lazyPage(HomePage) },
        { path: '403', element: lazyPage(ForbiddenPage) },
        { path: '*', element: lazyPage(NotFoundPage) },
      ],
    },
  ];
  return createBrowserRouter(routes);
}
