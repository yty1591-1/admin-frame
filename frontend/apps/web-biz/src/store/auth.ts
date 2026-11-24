import type { Recordable, UserInfo } from '@vben/types';

import { ref } from 'vue';
import { useRouter } from 'vue-router';

import { LOGIN_PATH } from '@vben/constants';
import { preferences } from '@vben/preferences';
import { resetAllStores, useAccessStore, useUserStore } from '@vben/stores';
import { resolveSafeRedirect } from '@vben/utils';

import { notification } from 'ant-design-vue';
import { defineStore } from 'pinia';

import {
  getMyPermissionsApi,
  getUserInfoApi,
  loginApi,
  logoutApi,
} from '#/api';
import { $t } from '#/locales';

export const useAuthStore = defineStore('auth', () => {
  const accessStore = useAccessStore();
  const userStore = useUserStore();
  const router = useRouter();

  const loginLoading = ref(false);

  /**
   * 密码登录。
   *
   * B 端登录没有 MFA / 强制改密中间态，拿到令牌即代表登录成功，
   * 因此在本方法内一次性完成「落库双令牌 → 拉资料与权限 → 跳转」。
   */
  async function authLogin(params: Recordable<any>) {
    loginLoading.value = true;
    try {
      const tokens = await loginApi({
        password: params.password,
        username: params.username,
      });
      await completeLogin(tokens);
    } finally {
      loginLoading.value = false;
    }
  }

  /** 统一落库双令牌 → 拉取资料与权限 → 跳转。 */
  async function completeLogin(tokens: {
    accessToken: string;
    refreshToken: string;
  }) {
    accessStore.setAccessToken(tokens.accessToken);
    accessStore.setRefreshToken(tokens.refreshToken);

    const userInfo = await fetchUserInfo();
    if (accessStore.loginExpired) {
      accessStore.setLoginExpired(false);
    }

    const redirect = router.currentRoute.value.query.redirect as
      | string
      | undefined;
    await router.push(
      redirect
        ? resolveSafeRedirect(redirect, preferences.app.defaultHomePath)
        : preferences.app.defaultHomePath,
    );

    if (userInfo?.realName) {
      notification.success({
        description: `${$t('authentication.loginSuccessDesc')}:${userInfo.realName}`,
        duration: 3,
        message: $t('authentication.loginSuccess'),
      });
    }
  }

  async function logout(redirect: boolean = true) {
    try {
      await logoutApi(accessStore.accessToken);
    } catch {
      // 不做任何处理
    }
    resetAllStores();
    accessStore.setLoginExpired(false);

    // 已经在登录页时不能再带 redirect：此时 currentRoute.fullPath 就是登录页本身，
    // 再编码一层会得到「登录页?redirect=编码后的登录页」，下一次又在这个基础上再包一层，
    // 反复登出会让 URL 逐跳变长，且没有上限。
    // On the login page the current route is the login page itself, so carrying it as
    // `redirect` would nest one more encoded layer on every repeat.
    const currentRoute = router.currentRoute.value;
    const alreadyOnLogin = currentRoute.path === LOGIN_PATH;

    // 回登录页带上当前路由地址
    await router.replace({
      path: LOGIN_PATH,
      query:
        redirect && !alreadyOnLogin
          ? { redirect: encodeURIComponent(currentRoute.fullPath) }
          : {},
    });
  }

  /**
   * 拉取当前主体资料与权限快照。
   *
   * 两个请求并行；角色来自权限快照而不是 /me，保持与 A 端一致的口径。
   */
  async function fetchUserInfo(): Promise<UserInfo> {
    const [userInfo, permissions] = await Promise.all([
      getUserInfoApi(),
      getMyPermissionsApi(),
    ]);
    const merged = { ...userInfo, roles: permissions.roles };
    userStore.setUserInfo(merged);
    accessStore.setAccessCodes(permissions.permissions);
    return merged;
  }

  function $reset() {
    loginLoading.value = false;
  }

  return {
    $reset,
    authLogin,
    fetchUserInfo,
    loginLoading,
    logout,
  };
});
