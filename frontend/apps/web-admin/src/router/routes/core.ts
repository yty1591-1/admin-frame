import type { RouteRecordRaw } from 'vue-router';

import { LOGIN_PATH } from '@vben/constants';
import { preferences } from '@vben/preferences';

import { $t } from '#/locales';

const BasicLayout = () => import('#/layouts/basic.vue');
const AuthPageLayout = () =>
  import('@vben/app-kernel/layouts/auth').then((m) => m.default);

/**
 * 全局 404 页面
 *
 * 无需权限、不参与菜单，兜底所有未匹配路径。
 */
const fallbackNotFoundRoute: RouteRecordRaw = {
  component: () => import('#/views/_core/fallback/not-found.vue'),
  meta: {
    hideInBreadcrumb: true,
    hideInMenu: true,
    hideInTab: true,
    title: '404',
  },
  name: 'FallbackNotFound',
  path: '/:path(.*)*',
};

/**
 * 基本路由（core routes）
 *
 * 这些路由不进入权限拦截：
 * - Root：基础布局容器，所有需要登录的页面都是它的子级；
 * - Authentication：登录页外壳。
 *
 * 注意：core 路由在守卫里会直接放行，不要把需要登录态的页面放进来。
 */
const coreRoutes: RouteRecordRaw[] = [
  {
    component: BasicLayout,
    meta: {
      hideInBreadcrumb: true,
      title: 'Root',
    },
    name: 'Root',
    path: '/',
    redirect: preferences.app.defaultHomePath,
    children: [],
  },
  {
    component: AuthPageLayout,
    meta: {
      hideInTab: true,
      title: 'Authentication',
    },
    name: 'Authentication',
    path: '/auth',
    redirect: LOGIN_PATH,
    children: [
      {
        name: 'Login',
        path: 'login',
        component: () => import('#/views/_core/authentication/login.vue'),
        meta: {
          title: $t('page.auth.login'),
        },
      },
    ],
  },
  {
    component: () => import('#/views/_core/install/index.vue'),
    meta: {
      hideInMenu: true,
      hideInTab: true,
      ignoreAccess: true,
      title: '配置中心安装',
    },
    name: 'ConfigInstall',
    path: '/setup/install',
  },
];

export { coreRoutes, fallbackNotFoundRoute };
