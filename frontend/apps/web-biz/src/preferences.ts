import {
  defineOverridesPreferences,
  preferences,
  updatePreferences,
} from '@vben/preferences';

/**
 * 品牌静态资源地址。
 *
 * 基于 `VITE_BASE` 拼接，保证子路径部署（如 `/biz/`）时资源仍可命中；
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
    defaultHomePath: '/member/list',
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
     * 刷新令牌：后端 refresh token 为一次性轮换，开启后由请求拦截器
     * 自动换新并同批替换双令牌，避免访问令牌过期即整页登出。
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
 * 迁移本机残留的偏好 / 演示登录态（与 web-admin 同口径）。
 */
export function migrateLegacyBrandAssets() {
  const currentLogo = preferences.logo.source ?? '';
  const currentAvatar = preferences.app.defaultAvatar ?? '';
  const usesRemoteAsset =
    currentLogo.includes('unpkg.com') || currentAvatar.includes('unpkg.com');
  const needsAccessModeFix = preferences.app.accessMode !== 'backend';
  const needsRefreshTokenFix = preferences.app.enableRefreshToken !== true;

  if (usesRemoteAsset || needsAccessModeFix || needsRefreshTokenFix) {
    updatePreferences({
      app: {
        ...(usesRemoteAsset ? { defaultAvatar: AVATAR_URL } : {}),
        ...(needsAccessModeFix ? { accessMode: 'backend' } : {}),
        ...(needsRefreshTokenFix ? { enableRefreshToken: true } : {}),
      },
      ...(usesRemoteAsset ? { logo: { source: LOGO_URL } } : {}),
    });
  }

  clearLegacyMockAccessToken();
}

function clearLegacyMockAccessToken() {
  if (typeof localStorage === 'undefined') {
    return;
  }
  for (let index = localStorage.length - 1; index >= 0; index -= 1) {
    const key = localStorage.key(index);
    if (!key || !key.includes('-core-access')) {
      continue;
    }
    try {
      const raw = localStorage.getItem(key);
      if (!raw) {
        continue;
      }
      const parsed = JSON.parse(raw) as {
        accessToken?: string;
        value?: { accessToken?: string };
      };
      const token = parsed.accessToken ?? parsed.value?.accessToken;
      if (!token || !isVbenMockAccessToken(token)) {
        continue;
      }
      localStorage.removeItem(key);
    } catch {
      localStorage.removeItem(key);
    }
  }
}

function isVbenMockAccessToken(token: string): boolean {
  const parts = token.split('.');
  if (parts.length < 2 || !parts[1]) {
    return false;
  }
  try {
    const json =
      typeof atob === 'function'
        ? atob(parts[1].replaceAll('-', '+').replaceAll('_', '/'))
        : '';
    const payload = JSON.parse(json) as {
      password?: string;
      username?: string;
    };
    return payload.username === 'vben' && payload.password === '123456';
  } catch {
    return false;
  }
}
