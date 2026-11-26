import { ADMIN_CAPABILITIES } from '@vben/app-kernel';
import { createAuthStore } from '@vben/app-kernel/store';

import {
  completePasswordChangeApi,
  confirmMfaBindingSessionApi,
  disableMfaApi,
  getMyPermissionsApi,
  getUserInfoApi,
  loginApi,
  logoutApi,
  mfaSetupApi,
  mfaSetupSessionApi,
  mfaVerifyApi,
} from '#/api';

/**
 * 登录态：四态登录、令牌落库与跳转流程在内核（FE-K-03 批次 D）。
 * 关闭的能力（B 端 MFA/改密）保留入口但调用即抛 KernelCapabilityError。
 */
export const useAuthStore = createAuthStore({
  authApi: {
    completePasswordChangeApi,
    confirmMfaBindingSessionApi,
    disableMfaApi,
    getMyPermissionsApi,
    getUserInfoApi,
    loginApi,
    logoutApi,
    mfaSetupApi,
    mfaSetupSessionApi,
    mfaVerifyApi,
  },
  capabilities: ADMIN_CAPABILITIES,
});
