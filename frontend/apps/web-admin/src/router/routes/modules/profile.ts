import type { RouteRecordRaw } from 'vue-router';

import { $t } from '#/locales';

/**
 * 个人中心
 *
 * 由布局右上角用户下拉菜单进入，因此不在菜单中显示。
 * 注意：accessMode=backend 时前端 modules 路由不参与生成，页面必须同时登记在
 * `iam_menu`（见迁移 000006_seed_profile_menu）；本文件仅在 accessMode=frontend 时生效。
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
