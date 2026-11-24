/**
 * 前端请求客户端：带鉴权、一次性刷新、统一错误呈现。
 *
 * 幂等头通过注入 enable 函数表达能力：A 端注入 ensureIdempotencyHeader，
 * B 端不注入（B 端写路径后端不要求幂等头）。
 */
import type { RequestClientConfig, RequestClientOptions } from '@vben/request';

import { preferences } from '@vben/preferences';
import {
  authenticateResponseInterceptor,
  defaultResponseInterceptor,
  errorMessageResponseInterceptor,
  RequestClient,
  resolveRequestErrorMessage,
} from '@vben/request';
import { useAccessStore } from '@vben/stores';

import { message } from 'ant-design-vue';

export interface AppRequestClientOptions {
  apiURL: string;
  /** 应用侧 auth store 访问器（登出/刷新失败时使用） */
  getAuthStore: () => { logout: () => Promise<void> | void };
  /** 刷新令牌 API（应用提供） */
  refreshTokenApi: (refreshToken: string) => Promise<{
    accessToken: string;
    refreshToken: string;
  }>;
  options?: RequestClientOptions;
  /** 写入 Idempotency-Key 的实现；不传表示关闭（FE-K-02 capabilities.idempotency） */
  ensureIdempotencyHeader?: (
    headers: RequestClientConfig['headers'],
    method?: string,
  ) => void;
}

function formatToken(token: null | string) {
  return token ? `Bearer ${token}` : null;
}

export function createAppRequestClient({
  apiURL,
  getAuthStore,
  refreshTokenApi,
  options,
  ensureIdempotencyHeader,
}: AppRequestClientOptions) {
  const client = new RequestClient({
    ...options,
    baseURL: apiURL,
  });

  async function doReAuthenticate() {
    console.warn('Access token or refresh token is invalid or expired. ');
    const accessStore = useAccessStore();
    const authStore = getAuthStore();
    accessStore.setAccessToken(null);
    if (
      preferences.app.loginExpiredMode === 'modal' &&
      accessStore.isAccessChecked
    ) {
      accessStore.setLoginExpired(true);
    } else {
      await authStore.logout();
    }
  }

  /**
   * 一次性轮换：refresh token 换回新令牌对，失败时清空双令牌。
   */
  async function doRefreshToken() {
    const accessStore = useAccessStore();
    const refreshToken = accessStore.refreshToken;
    if (!refreshToken) {
      throw new Error('missing refresh token');
    }
    try {
      const tokens = await refreshTokenApi(refreshToken);
      accessStore.setAccessToken(tokens.accessToken);
      accessStore.setRefreshToken(tokens.refreshToken);
      return tokens.accessToken;
    } catch (error) {
      accessStore.setAccessToken(null);
      accessStore.setRefreshToken(null);
      throw error;
    }
  }

  // 请求头处理（幂等头按能力注入）
  client.addRequestInterceptor({
    fulfilled: async (config) => {
      const accessStore = useAccessStore();

      config.headers.Authorization = formatToken(accessStore.accessToken);
      config.headers['Accept-Language'] = preferences.app.locale;
      ensureIdempotencyHeader?.(config.headers, config.method);
      return config;
    },
  });

  // 响应数据格式
  client.addResponseInterceptor(
    defaultResponseInterceptor({
      codeField: 'code',
      dataField: 'data',
      successCode: 0,
    }),
  );

  // token 过期处理
  client.addResponseInterceptor(
    authenticateResponseInterceptor({
      client,
      doReAuthenticate,
      doRefreshToken,
      enableRefreshToken: preferences.app.enableRefreshToken,
      formatToken,
    }),
  );

  // 统一错误呈现：业务码映射 + trace_id，不直出服务端 message
  client.addResponseInterceptor(
    errorMessageResponseInterceptor((msg: string, error) => {
      message.error(resolveRequestErrorMessage(error?.response?.data, msg));
    }),
  );

  /** 无鉴权拦截器的客户端（登录/登出/刷新等），仍可按能力注入幂等头。 */
  const baseRequestClient = new RequestClient({ baseURL: apiURL });
  if (ensureIdempotencyHeader) {
    baseRequestClient.addRequestInterceptor({
      fulfilled: async (config) => {
        ensureIdempotencyHeader(config.headers, config.method);
        return config;
      },
    });
  }

  return {
    baseRequestClient,
    requestClient: client,
  };
}
