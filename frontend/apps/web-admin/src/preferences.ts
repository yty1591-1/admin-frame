import {
  defineOverridesPreferences,
  preferences,
  updatePreferences,
} from '@vben/preferences';

/**
 * 品牌静态资源地址。
 *
 * 基于 `VITE_BASE` 拼接，保证子路径部署（如 `/admin/`）时资源仍可命中；
 * 资源位于 app 的 `public/brand/`，不走外网 CDN。
 */
const LOGO_URL = `${import.meta.env.BASE_URL}brand/logo.svg`;
const AVATAR_URL = `${import.meta.env.BASE_URL}brand/avatar.svg`;

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
     * 默认头像，用于用户未设置头像时的占位。
     */
    defaultAvatar: AVATAR_URL,
    /**
     * 权限模式：
     * - frontend：菜单与路由由前端 route modules 定义，按用户角色过滤；
     * - backend：菜单由后端 GET /v1/iam/me/menus 下发并按权限点裁剪，前端只做组件映射。
     *
     * admin-frame 已提供 iam_menu 菜单资源与裁剪接口，因此采用 backend；
     * 菜单由后端种子的 route name / component 驱动，必须与 views 目录保持一致。
     */
    accessMode: 'backend',
    /**
     * admin-frame 刷新令牌为一次性轮换：必须在拦截器内用 refresh token 换新，
     * 因此显式开启 Vben 的 401 自动刷新。
     */
    enableRefreshToken: true,
  },
  logo: {
    source: LOGO_URL,
  },
  copyright: {
    companyName: 'Admin Frame',
    companySiteLink: '',
    date: '2026',
    icp: '',
    icpLink: '',
  },
});

/**
 * 把历史缓存中的 CDN 品牌资源改写为本地资源。
 *
 * 背景：偏好设置按 namespace 持久化到 localStorage，且合并时缓存优先于
 * `overridesPreferences`（见 `@vben-core/preferences` 的 `initPreferences`）。
 * 若不做迁移，已访问过的浏览器仍会继续使用旧地址，表现为「改了代码但没生效」。
 *
 * 幂等：当前值已是本地路径时直接返回，不会产生多余写入。
 * 注意：不 bump `VITE_APP_VERSION` 清理缓存，因为那样会连带清掉 token 存储
 * 导致全员重新登录。
 */
export function migrateLegacyBrandAssets() {
  const currentLogo = preferences.logo.source ?? '';
  const currentAvatar = preferences.app.defaultAvatar ?? '';
  const usesRemoteAsset =
    currentLogo.includes('unpkg.com') || currentAvatar.includes('unpkg.com');

  if (!usesRemoteAsset) {
    return;
  }

  updatePreferences({
    app: { defaultAvatar: AVATAR_URL },
    logo: { source: LOGO_URL },
  });
}
