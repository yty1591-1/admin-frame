import type { RouteRecordRaw } from 'vue-router';

import { $t } from '#/locales';

/**
 * 系统管理模块
 *
 * 关于权限：前端权限模式（preferences.app.accessMode = 'frontend'）下，
 * 在 meta 上声明 authority 即可按角色过滤菜单与路由，例如
 *
 *   meta: { authority: ['super', 'admin'], ... }
 *
 * 用户角色与数组有交集时才生成该节点。注意：defaultHomePath 指向的页面
 * 必须对所有可登录角色可见，否则该角色登录后会落到 403 / 404。
 * 因此当前示例不限制角色，只保留注释说明。
 */
const routes: RouteRecordRaw[] = [
  {
    meta: {
      icon: 'lucide:settings',
      order: 10,
      title: $t('page.system.title'),
    },
    name: 'System',
    path: '/system',
    children: [
      {
        name: 'SystemRole',
        path: 'role',
        component: () => import('#/views/system/role/index.vue'),
        meta: {
          icon: 'lucide:shield-check',
          title: $t('page.system.role'),
        },
      },
    ],
  },
];

export default routes;
