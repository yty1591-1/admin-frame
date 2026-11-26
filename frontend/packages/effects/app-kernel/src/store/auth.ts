import type { Recordable, UserInfo } from '@vben/types';

import type { LoginOutcome, MfaSetupData, TokenPairVM } from '../api/core/auth';
import type { KernelCapabilities } from '../types';

import { ref } from 'vue';
import { useRouter } from 'vue-router';

import { LOGIN_PATH } from '@vben/constants';
import { $t } from '@vben/locales';
import { preferences } from '@vben/preferences';
import { resetAllStores, useAccessStore, useUserStore } from '@vben/stores';
import { resolveSafeRedirect } from '@vben/utils';

import { notification } from 'ant-design-vue';
import { defineStore } from 'pinia';

import { KernelCapabilityError } from '../types';

/** 认证 API 的最小面（由应用注入，避免内核反向依赖 app 的 #/api）。 */
export interface AuthApiLike {
  completePasswordChangeApi: (params: {
    newPassword: string;
    ticket: string;
  }) => Promise<LoginOutcome>;
  confirmMfaBindingSessionApi: (
    code: string,
    accessToken?: null | string,
  ) => Promise<void>;
  disableMfaApi: (code: string, accessToken?: null | string) => Promise<void>;
  getMyPermissionsApi: () => Promise<{
    permissions: string[];
    roles: string[];
  }>;
  getUserInfoApi: () => Promise<UserInfo>;
  loginApi: (data: {
    password: string;
    username: string;
  }) => Promise<LoginOutcome>;
  logoutApi: (accessToken?: null | string) => Promise<void>;
  mfaSetupApi: (ticket: string) => Promise<MfaSetupData>;
  mfaSetupSessionApi: () => Promise<MfaSetupData>;
  mfaVerifyApi: (params: {
    code: string;
    mfaSetupTicket?: string;
    mfaTicket?: string;
  }) => Promise<TokenPairVM>;
}

export interface CreateAuthStoreOptions {
  authApi: AuthApiLike;
  capabilities: KernelCapabilities;
}

/**
 * 构建 auth store。
 *
 * A/B 端的差异收敛为能力开关：关闭的能力保留方法入口，
 * 调用即抛 KernelCapabilityError（fail-closed，不静默假成功）。
 */
export function createAuthStore({
  authApi,
  capabilities,
}: CreateAuthStoreOptions) {
  function requireCapability(capability: keyof KernelCapabilities) {
    if (!capabilities[capability]) {
      throw new KernelCapabilityError(capability);
    }
  }

  return defineStore('auth', () => {
    const accessStore = useAccessStore();
    const userStore = useUserStore();
    const router = useRouter();

    const loginLoading = ref(false);

    /**
     * 密码登录。
     *
     * 返回四态：拿到令牌时已在本方法内完成落库与跳转；
     * 改密 / MFA 中间态由登录页接管后续步骤，任何中间态都不会写入令牌。
     */
    async function authLogin(params: Recordable<any>): Promise<LoginOutcome> {
      loginLoading.value = true;
      try {
        const outcome = await authApi.loginApi({
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

    /** 强制改密：凭一次性票据提交新密码，结果仍可能是中间态。 */
    async function completePasswordChange(
      ticket: string,
      newPassword: string,
    ): Promise<LoginOutcome> {
      requireCapability('passwordChange');
      loginLoading.value = true;
      try {
        const outcome = await authApi.completePasswordChangeApi({
          newPassword,
          ticket,
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
      requireCapability('mfa');
      loginLoading.value = true;
      try {
        const tokens = await authApi.mfaVerifyApi({ code, mfaTicket: ticket });
        await completeLogin(tokens);
      } finally {
        loginLoading.value = false;
      }
    }

    /** 强制绑定：初始化 TOTP，返回密钥、二维码 URI 与恢复码（仅本次可见）。 */
    async function startMfaBinding(ticket: string): Promise<MfaSetupData> {
      requireCapability('mfaBinding');
      loginLoading.value = true;
      try {
        return await authApi.mfaSetupApi(ticket);
      } finally {
        loginLoading.value = false;
      }
    }

    /** 强制绑定：首码校验通过后后端直接签发令牌对。 */
    async function confirmMfaBinding(ticket: string, code: string) {
      requireCapability('mfaBinding');
      loginLoading.value = true;
      try {
        const tokens = await authApi.mfaVerifyApi({
          code,
          mfaSetupTicket: ticket,
        });
        await completeLogin(tokens);
      } finally {
        loginLoading.value = false;
      }
    }

    /** 已登录账号初始化 TOTP 绑定。 */
    async function startMfaBindingForAccount(): Promise<MfaSetupData> {
      requireCapability('mfaBinding');
      return await authApi.mfaSetupSessionApi();
    }

    /** 已登录账号用动态码确认首次绑定。 */
    async function confirmMfaBindingForAccount(code: string) {
      requireCapability('mfaBinding');
      await authApi.confirmMfaBindingSessionApi(code, accessStore.accessToken);
    }

    /**
     * 解绑当前账号 MFA。
     *
     * 成功后服务端撤销全部会话，这里统一走登出，避免继续使用失效令牌。
     */
    async function disableMfaForAccount(code: string) {
      requireCapability('mfa');
      await authApi.disableMfaApi(code, accessStore.accessToken);
      await logout();
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
        await authApi.logoutApi(accessStore.accessToken);
      } catch {
        // 登出失败不阻断本地清理
      }
      resetAllStores();
      accessStore.setLoginExpired(false);

      // 已经在登录页时不能再带 redirect：否则反复登出会让 URL 逐跳变长、没有上限。
      const currentRoute = router.currentRoute.value;
      const alreadyOnLogin = currentRoute.path === LOGIN_PATH;

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
        authApi.getUserInfoApi(),
        authApi.getMyPermissionsApi(),
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
      completePasswordChange,
      confirmMfaBinding,
      confirmMfaBindingForAccount,
      disableMfaForAccount,
      fetchUserInfo,
      loginLoading,
      logout,
      startMfaBinding,
      startMfaBindingForAccount,
      verifyMfaChallenge,
    };
  });
}
