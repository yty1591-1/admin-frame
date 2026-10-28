import type { Recordable, UserInfo } from '@vben/types';

import type { LoginOutcome, MfaSetupData, TokenPairVM } from '#/api/core/auth';

import { ref } from 'vue';
import { useRouter } from 'vue-router';

import { LOGIN_PATH } from '@vben/constants';
import { preferences } from '@vben/preferences';
import { resetAllStores, useAccessStore, useUserStore } from '@vben/stores';

import { notification } from 'ant-design-vue';
import { defineStore } from 'pinia';

import {
  getMyPermissionsApi,
  getUserInfoApi,
  loginApi,
  logoutApi,
  mfaSetupApi,
  mfaVerifyApi,
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
   * 返回三态：拿到令牌时已在本方法内完成落库与跳转；
   * MFA 中间态由登录页接管后续步骤，任何中间态都不会写入令牌。
   */
  async function authLogin(params: Recordable<any>): Promise<LoginOutcome> {
    loginLoading.value = true;
    try {
      const outcome = await loginApi({
        password: params.password,
        username: params.username,
      });
      if (outcome.kind === 'token') {
        await completeLogin(outcome.tokens);
      }
      return outcome;
    } finally {
      loginLoading.value = false;
    }
  }

  /** 已完成密码校验的账号：用一次性票据完成第二步 MFA。 */
  async function verifyMfaChallenge(ticket: string, code: string) {
    loginLoading.value = true;
    try {
      const tokens = await mfaVerifyApi({ code, mfaTicket: ticket });
      await completeLogin(tokens);
    } finally {
      loginLoading.value = false;
    }
  }

  /** 强制绑定：初始化 TOTP，返回密钥、二维码 URI 与恢复码（仅本次可见）。 */
  async function startMfaBinding(ticket: string): Promise<MfaSetupData> {
    loginLoading.value = true;
    try {
      return await mfaSetupApi(ticket);
    } finally {
      loginLoading.value = false;
    }
  }

  /** 强制绑定：首码校验通过后后端直接签发令牌对。 */
  async function confirmMfaBinding(ticket: string, code: string) {
    loginLoading.value = true;
    try {
      const tokens = await mfaVerifyApi({ code, mfaSetupTicket: ticket });
      await completeLogin(tokens);
    } finally {
      loginLoading.value = false;
    }
  }

  /** 统一落库双令牌 → 拉取资料与权限 → 跳转。 */
  async function completeLogin(tokens: TokenPairVM) {
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
      redirect ? decodeURIComponent(redirect) : preferences.app.defaultHomePath,
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
    confirmMfaBinding,
    fetchUserInfo,
    loginLoading,
    logout,
    startMfaBinding,
    verifyMfaChallenge,
  };
});
