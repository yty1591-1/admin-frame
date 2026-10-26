import type { PermissionChecker } from '@admin-frame/auth';
import type { MessageKey, Translator } from '@admin-frame/i18n';
import { PageLoading, type AppShellMenuEntry } from '@admin-frame/ui';
import { Suspense, lazy, type ComponentType, type ReactNode } from 'react';
import type { RouteObject } from 'react-router';
import {
  RequirePermission,
  checkPermission,
  type PermissionRequirement,
} from '../entities/permission';

export type AppRouteMeta = {
  /** 菜单/页面标题的文案 key（§11.4）。 */
  titleKey: MessageKey;
  /** 权限码（任一满足）；缺省表示仅需登录（§7.1）。 */
  permission?: PermissionRequirement;
  /** 是否进入主导航；403/404 等状态页不进入。 */
  menu?: boolean;
  menuOrder?: number;
};

type ShellRoute = {
  /** 相对 `/` 的路径；空字符串表示 index 路由。 */
  path: string;
  Page: ComponentType;
  meta: AppRouteMeta;
};

const HomePage = lazy(async () => import('../pages/HomePage'));
const PermissionCatalogPage = lazy(async () => import('../pages/PermissionCatalogPage'));
const IamRoleListPage = lazy(async () => import('../pages/IamRoleListPage'));
const ForbiddenPage = lazy(async () => import('../pages/ForbiddenPage'));
const NotFoundPage = lazy(async () => import('../pages/NotFoundPage'));

/**
 * 路由表是权限、菜单与懒加载的单一描述源（§7.1）。
 * 权限码必须与 docs/iam/PERMISSIONS.md 保持一致，禁止在页面内硬编码。
 */
const SHELL_ROUTES: readonly ShellRoute[] = [
  {
    path: '',
    Page: HomePage,
    meta: { titleKey: 'nav.home', menu: true, menuOrder: 10 },
  },
  {
    path: 'iam/permissions',
    Page: PermissionCatalogPage,
    meta: {
      titleKey: 'nav.permissionCatalog',
      permission: 'iam:permission:read',
      menu: true,
      menuOrder: 20,
    },
  },
  {
    path: 'iam/roles',
    Page: IamRoleListPage,
    meta: {
      titleKey: 'nav.iamRoles',
      permission: 'iam:role:read',
      menu: true,
      menuOrder: 30,
    },
  },
];

/** 状态页不参与权限判定，避免权限服务故障时无法看到 404/403。 */
const STATUS_ROUTES: readonly ShellRoute[] = [
  { path: '403', Page: ForbiddenPage, meta: { titleKey: 'nav.forbidden' } },
  { path: '*', Page: NotFoundPage, meta: { titleKey: 'nav.notFound' } },
];

function renderPage(Page: ComponentType): ReactNode {
  return (
    <Suspense fallback={<PageLoading />}>
      <Page />
    </Suspense>
  );
}

function toRouteObject(route: ShellRoute, guarded: boolean): RouteObject {
  const page = renderPage(route.Page);
  return {
    ...(route.path === '' ? { index: true } : { path: route.path }),
    element: guarded ? (
      <RequirePermission permission={route.meta.permission}>{page}</RequirePermission>
    ) : (
      page
    ),
    handle: { meta: route.meta },
  };
}

/** 外壳子路由：业务页统一经过权限守卫（快照未就绪等待、失败 fail-closed）。 */
export function buildShellRouteObjects(): RouteObject[] {
  return [
    ...SHELL_ROUTES.map((route) => toRouteObject(route, true)),
    ...STATUS_ROUTES.map((route) => toRouteObject(route, false)),
  ];
}

/** 菜单 = 路由表 + 权限快照过滤；结构以路由表为准（D-04）。 */
export function buildMenuEntries(checker: PermissionChecker, t: Translator): AppShellMenuEntry[] {
  return SHELL_ROUTES.filter((route) => route.meta.menu === true)
    .filter((route) => checkPermission(checker, route.meta.permission))
    .sort((left, right) => (left.meta.menuOrder ?? 0) - (right.meta.menuOrder ?? 0))
    .map((route) => ({
      key: route.path === '' ? '/' : `/${route.path}`,
      label: t(route.meta.titleKey),
    }));
}
