import { createAccessGenerator } from '@vben/app-kernel/router';

import { getAllMenusApi } from '#/api';
import { BasicLayout, IFrameView } from '#/layouts';

const forbiddenComponent = () => import('#/views/_core/fallback/forbidden.vue');

/** 权限路由生成器（菜单拉取、页面映射、布局映射由应用提供） */
const generateAccess = createAccessGenerator({
  fetchMenuList: getAllMenusApi,
  forbiddenComponent,
  layoutMap: {
    BasicLayout,
    IFrameView,
  },
  pageMap: import.meta.glob('../views/**/*.vue'),
});

export { generateAccess };
