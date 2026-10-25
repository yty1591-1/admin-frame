import type { HttpClient } from '../../http/client';
import { loginOutcomeSchema, tokenPairSchema } from '../../schemas';
import type { LoginOutcome, TokenPair } from '../../types';

export type AdminAuthApi = {
  /** 密码登录；返回令牌对或 MFA 挑战（调用方必须窄化）。 */
  login(subject: string, password: string): Promise<LoginOutcome>;
  /** 一次性刷新令牌轮换。 */
  refresh(refreshToken: string): Promise<TokenPair>;
  logout(): Promise<void>;
  logoutAll(): Promise<void>;
};

export function createAdminAuthApi(http: HttpClient): AdminAuthApi {
  return {
    login: (subject, password) =>
      http.request<LoginOutcome>('/admin/v1/auth/login', {
        method: 'POST',
        auth: false,
        body: { subject, password },
        schema: loginOutcomeSchema,
        retry: 'none',
      }),
    refresh: (refreshToken) =>
      http.request<TokenPair>('/admin/v1/auth/refresh', {
        method: 'POST',
        auth: false,
        body: { refresh_token: refreshToken },
        schema: tokenPairSchema,
        retry: 'none',
      }),
    logout: () => http.requestVoid('/admin/v1/auth/logout', { method: 'POST' }),
    logoutAll: () => http.requestVoid('/admin/v1/auth/logout-all', { method: 'POST' }),
  };
}
