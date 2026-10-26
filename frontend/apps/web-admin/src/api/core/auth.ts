import { baseRequestClient, requestClient } from '#/api/request';

/**
 * 认证接口
 *
 * 当前对接 Nitro Mock（api 前缀由 .env 的 VITE_GLOB_API_URL 决定）。
 * 切换 admin-frame 后端时，需要按下表调整本文件：
 *
 * | 前端函数           | admin-frame 契约                        | 说明                              |
 * |--------------------|----------------------------------------|-----------------------------------|
 * | loginApi           | POST /admin/v1/auth/login              | 入参 subject + password            |
 * | refreshTokenApi    | POST /admin/v1/auth/refresh            | 入参 refresh_token（放入 body）     |
 * | logoutApi          | POST /admin/v1/auth/logout             | 需要 Bearer access_token           |
 * | getAccessCodesApi  | GET  /admin/v1/iam/me/permissions      | 取当前主体 permissions 数组         |
 *
 * 登录响应字段后端为 access_token / refresh_token（ snake_case ），
 * 与本文件 LoginResult 的 accessToken 不一致，接入时需在 api 层做一次映射。
 * 登录响应还有 MFAChallenge / MFABindingRequired 两种中间态，logout 等端点为 204。
 * 完整改造清单与验收标准见 docs/FRONTEND_INTEGRATION_REVIEW.md。
 * 后端成功响应为 { code, data, message, trace_id }，其中 code=0 视为成功，
 * 与 src/api/request.ts 中的默认拦截器约定一致，无需额外适配。
 */
export namespace AuthApi {
  /** 登录接口参数 */
  export interface LoginParams {
    password?: string;
    username?: string;
  }

  /** 登录接口返回值 */
  export interface LoginResult {
    accessToken: string;
  }

  export interface RefreshTokenResult {
    data: string;
    status: number;
  }
}

/**
 * 登录
 */
export async function loginApi(data: AuthApi.LoginParams) {
  return requestClient.post<AuthApi.LoginResult>('/auth/login', data);
}

/**
 * 刷新accessToken
 */
export async function refreshTokenApi() {
  return baseRequestClient.post<AuthApi.RefreshTokenResult>(
    '/auth/refresh',
    undefined,
    {
      withCredentials: true,
    },
  );
}

/**
 * 退出登录
 */
export async function logoutApi() {
  return baseRequestClient.post('/auth/logout', undefined, {
    withCredentials: true,
  });
}

/**
 * 获取用户权限码
 */
export async function getAccessCodesApi() {
  return requestClient.get<string[]>('/auth/codes');
}
