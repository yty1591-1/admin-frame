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
    defaultHomePath: '/system/role',
    /**
     * 权限模式：
     * - frontend：菜单与路由由前端 route modules 定义，按用户角色过滤；
     * - backend：菜单由后端 GET /v1/iam/me/menus 下发并按权限点裁剪，前端只做组件映射。
     *
     * admin-frame 已提供 iam_menu 菜单资源与裁剪接口，因此采用 backend；
     * 菜单由后端种子的 route name / component 驱动，必须与 views 目录保持一致。
     */
    accessMode: 'backend',
  },
  copyright: {
    companyName: 'Admin Frame',
    companySiteLink: '',
    date: '2026',
    icp: '',
    icpLink: '',
  },
});
