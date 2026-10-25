import type { HttpClient } from '../../http/client';
import { loginOutcomeSchema, tokenPairSchema } from '../../schemas';
import type { LoginOutcome, TokenPair } from '../../types';

export type BizAuthApi = {
  login(subject: string, password: string): Promise<LoginOutcome>;
  refresh(refreshToken: string): Promise<TokenPair>;
  logout(): Promise<void>;
  logoutAll(): Promise<void>;
};

export function createBizAuthApi(http: HttpClient): BizAuthApi {
  return {
    login: (subject, password) =>
      http.request<LoginOutcome>('/biz/v1/auth/login', {
        method: 'POST',
        auth: false,
        body: { subject, password },
        schema: loginOutcomeSchema,
        retry: 'none',
      }),
    refresh: (refreshToken) =>
      http.request<TokenPair>('/biz/v1/auth/refresh', {
        method: 'POST',
        auth: false,
        body: { refresh_token: refreshToken },
        schema: tokenPairSchema,
        retry: 'none',
      }),
    logout: () => http.requestVoid('/biz/v1/auth/logout', { method: 'POST' }),
    logoutAll: () => http.requestVoid('/biz/v1/auth/logout-all', { method: 'POST' }),
  };
}
