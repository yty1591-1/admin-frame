import type { RouteRecordRaw } from 'vue-router';

import { $t } from '#/locales';

/**
 * 个人中心
 *
 * 由布局右上角用户下拉菜单进入，因此不在菜单中显示。
 * 该路由需要登录态，所以放在 modules 下走权限拦截，而不是 core 路由。
 */
const routes: RouteRecordRaw[] = [
  {
    name: 'Profile',
    path: '/profile',
    component: () => import('#/views/_core/profile/index.vue'),
    meta: {
      hideInMenu: true,
      icon: 'lucide:user',
      title: $t('page.profile.title'),
    },
  },
];

export default routes;
