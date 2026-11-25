import { ADMIN_CAPABILITIES } from '@vben/app-kernel';
import { createAuthApi } from '@vben/app-kernel/api/core/auth';

import { baseRequestClient, requestClient } from '#/api/request';

export * from '@vben/app-kernel/api/core/auth';

/**
 * 认证 API：契约实现与能力开关在内核，本文件只负责注入请求客户端（FE-K-03 批次 D）。
 */
export const {
  changePasswordApi,
  completePasswordChangeApi,
  confirmMfaBindingSessionApi,
  disableMfaApi,
  getAccessCodesApi,
  getMyPermissionsApi,
  listMySessionsApi,
  loginApi,
  logoutApi,
  mfaSetupApi,
  mfaSetupSessionApi,
  mfaVerifyApi,
  refreshTokenApi,
  revokeAllMySessionsApi,
  revokeMySessionApi,
} = createAuthApi({
  baseRequestClient,
  capabilities: ADMIN_CAPABILITIES,
  requestClient,
});
