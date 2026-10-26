import { defineOverridesPreferences } from '@vben/preferences';

/**
 * 应用配置
 *
 * 只需要覆盖需要变更的配置，未覆盖的项自动使用框架默认值。
 * 注意：更改配置后请清空浏览器缓存（偏好设置基于 namespace 持久化），否则可能不生效。
 */
export const overridesPreferences = defineOverridesPreferences({
  app: {
    /**
     * 应用名称，除标题外也用于登录页、水印等展示位置。
     */
    name: import.meta.env.VITE_APP_TITLE,
    /**
     * 登录后的默认首页，同时也是根路由的 redirect 目标。
     */
    defaultHomePath: '/member/list',
    /**
     * 权限模式：
     * - frontend：菜单与路由由前端 route modules 定义，按用户角色过滤（当前采用）；
     * - backend：菜单由后端接口下发，前端只做组件映射。
     *
     * admin-frame 后端当前没有菜单资源模型，因此保持 frontend，
     * 后续新增菜单资源后再切换为 backend。
     */
    accessMode: 'frontend',
  },
  copyright: {
    companyName: 'Admin Frame',
    companySiteLink: '',
    date: '2026',
    icp: '',
    icpLink: '',
  },
});
