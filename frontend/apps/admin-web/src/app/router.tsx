import type { AppRuntimeConfig } from '@admin-frame/shared';
import { PageLoading } from '@admin-frame/ui';
import { Suspense, lazy, type ComponentType, type ReactNode } from 'react';
import { createBrowserRouter, type RouteObject } from 'react-router';
import { AppLayout } from './AppLayout';
import { RequireAuth } from './auth/RequireAuth';
import { buildShellRouteObjects } from './routes';

const LoginPage = lazy(async () => import('../pages/LoginPage'));

function lazyPage(Page: ComponentType): ReactNode {
  return (
    <Suspense fallback={<PageLoading />}>
      <Page />
    </Suspense>
  );
}

/** 路由即权限/菜单/懒加载的单一描述源（§7.1）；外壳子路由见 app/routes.tsx。 */
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
      children: buildShellRouteObjects(),
    },
  ];
  return createBrowserRouter(routes);
}
