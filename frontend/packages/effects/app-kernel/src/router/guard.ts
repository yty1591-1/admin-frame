import type {
  RouteLocationResolvedGeneric,
  Router,
  RouteRecordNameGeneric,
  RouteRecordRaw,
} from 'vue-router';

import type { GenerateMenuAndRoutesOptions, UserInfo } from '@vben/types';

import { LOGIN_PATH } from '@vben/constants';
import { preferences } from '@vben/preferences';
import { useAccessStore, useUserStore } from '@vben/stores';
import { resolveSafeRedirect, startProgress, stopProgress } from '@vben/utils';

export interface RouterGuardOptions {
  /** 有权限校验的路由列表 */
  accessRoutes: RouteRecordRaw[];
  /** 基本路由名（不走权限拦截） */
  coreRouteNames: RouteRecordNameGeneric[];
  /** 权限路由生成器（各应用注入 generateAccess） */
  generateAccess: (options: GenerateMenuAndRoutesOptions) => Promise<{
    accessibleMenus: unknown;
    accessibleRoutes: RouteRecordRaw[];
  }>;
  /** 取应用侧 auth store（用于拉取用户信息） */
  getAuthStore: () => { fetchUserInfo: () => Promise<UserInfo> };
}

/**
 * 通用守卫配置
 */
function setupCommonGuard(router: Router) {
  // 记录已经加载的页面
  const loadedPaths = new Set<string>();

  router.beforeEach((to) => {
    to.meta.loaded = loadedPaths.has(to.path);

    // 页面加载进度条
    if (!to.meta.loaded && preferences.transition.progress) {
      startProgress();
    }
    return true;
  });

  router.afterEach((to) => {
    // 记录页面是否加载,如果已经加载，后续的页面切换动画等效果不在重复执行
    loadedPaths.add(to.path);

    // 关闭页面加载进度条
    if (preferences.transition.progress) {
      stopProgress();
    }
  });
}

/**
 * 权限访问守卫配置
 */
function setupAccessGuard(router: Router, options: RouterGuardOptions) {
  const { accessRoutes, coreRouteNames, generateAccess, getAuthStore } =
    options;

  router.beforeEach(async (to, from) => {
    const accessStore = useAccessStore();
    const userStore = useUserStore();
    const authStore = getAuthStore();

    // 基本路由，这些路由不需要进入权限拦截
    if (coreRouteNames.includes(to.name)) {
      if (to.path === LOGIN_PATH && accessStore.accessToken) {
        const homePath =
          userStore.userInfo?.homePath || preferences.app.defaultHomePath;
        // 已登录再进登录页：回跳也要避开未注册路径（如历史 /dashboard）。
        return pickRegisteredPath(router, [
          resolveSafeRedirect(to.query?.redirect, homePath),
          homePath,
          preferences.app.defaultHomePath,
        ]);
      }
      return true;
    }

    // accessToken 检查
    if (!accessStore.accessToken) {
      // 明确声明忽略权限访问权限，则可以访问
      if (to.meta.ignoreAccess) {
        return true;
      }

      // 没有访问权限，跳转登录页面
      if (to.fullPath !== LOGIN_PATH) {
        return {
          path: LOGIN_PATH,
          // 如不需要，直接删除 query
          query:
            to.fullPath === preferences.app.defaultHomePath
              ? {}
              : { redirect: encodeURIComponent(to.fullPath) },
          // 携带当前跳转的页面，登录后重新跳转该页面
          replace: true,
        };
      }
      return to;
    }

    // 是否已经生成过动态路由
    if (accessStore.isAccessChecked) {
      return true;
    }

    // 生成路由表
    // 当前登录用户拥有的角色标识列表
    const userInfo = userStore.userInfo || (await authStore.fetchUserInfo());
    const userRoles = userInfo.roles ?? [];

    // 生成菜单和路由
    const { accessibleMenus, accessibleRoutes } = await generateAccess({
      roles: userRoles,
      router,
      // 则会在菜单中显示，但是访问会被重定向到403
      routes: accessRoutes,
    });

    // 保存菜单信息和路由信息
    accessStore.setAccessMenus(accessibleMenus as never);
    accessStore.setAccessRoutes(accessibleRoutes);
    accessStore.setIsAccessChecked(true);
    const homePath = userInfo.homePath || preferences.app.defaultHomePath;
    // 优先回跳登录前地址；若目标未注册（如残留的 /dashboard），回落到首页，
    // 避免「登录成功 → 立刻 404」。
    const preferred = resolveSafeRedirect(
      from.query.redirect ??
        (to.path === preferences.app.defaultHomePath ? homePath : to.fullPath),
      homePath,
    );
    const redirectPath = pickRegisteredPath(router, [
      preferred,
      homePath,
      preferences.app.defaultHomePath,
      firstAccessiblePath(accessibleRoutes),
    ]);

    return {
      ...router.resolve(redirectPath),
      replace: true,
    };
  });
}

/** 判断 resolve 结果是否落在全局 404 兜底路由上。 */
function isNotFoundLocation(resolved: RouteLocationResolvedGeneric): boolean {
  if (resolved.name === 'FallbackNotFound') {
    return true;
  }
  const matched = resolved.matched ?? [];
  if (matched.length === 0) {
    return true;
  }
  return matched.some((record) => record.name === 'FallbackNotFound');
}

/**
 * 从候选路径中选出第一条已注册业务路由；都未命中则退回第一个候选（通常是首页）。
 */
function pickRegisteredPath(router: Router, candidates: Array<null | string>) {
  const unique = [
    ...new Set(
      candidates
        .map((item) => item?.trim())
        .filter((item): item is string => Boolean(item)),
    ),
  ];
  for (const path of unique) {
    if (!isNotFoundLocation(router.resolve(path))) {
      return path;
    }
  }
  return unique[0] || preferences.app.defaultHomePath;
}

/** 取后端菜单树中第一个可访问叶子路径，供首页也未注册时兜底。 */
function firstAccessiblePath(routes: RouteRecordRaw[]): string {
  for (const route of routes) {
    const children = route.children ?? [];
    if (children.length > 0) {
      const childPath = firstAccessiblePath(children);
      if (!childPath) {
        continue;
      }
      if (childPath.startsWith('/')) {
        return childPath;
      }
      const parent = route.path || '';
      if (parent.endsWith('/')) {
        return `${parent}${childPath}`;
      }
      return `${parent}/${childPath}`;
    }
    if (route.path) {
      return route.path;
    }
  }
  return '';
}

/**
 * 项目守卫配置
 */
export function createRouterGuard(router: Router, options: RouterGuardOptions) {
  /** 通用 */
  setupCommonGuard(router);
  /** 权限访问 */
  setupAccessGuard(router, options);
}
