import type { HttpClient } from '../../http/client';
import {
  authSessionListDataSchema,
  loginOutcomeSchema,
  mfaSetupDataSchema,
  tokenPairSchema,
} from '../../schemas';
import type { AuthSessionListData, LoginOutcome, MFASetupData, TokenPair } from '../../types';

export type AdminAuthApi = {
  /** 密码登录；返回令牌对或 MFA 挑战（调用方必须窄化）。 */
  login(subject: string, password: string): Promise<LoginOutcome>;
  /** 一次性刷新令牌轮换。 */
  refresh(refreshToken: string): Promise<TokenPair>;
  /** 两步登录：使用登录返回的 `mfa_ticket` 换取令牌（票据一次性消费）。 */
  verifyMfaLogin(mfaTicket: string, code: string): Promise<TokenPair>;
  /**
   * 强制绑定完成后提交首码：使用登录返回的 `mfa_setup_ticket` 换取令牌（§7.2.1）。
   * 票据一次性消费，首码错误同样作废。
   */
  verifyMfaSetup(mfaSetupTicket: string, code: string): Promise<TokenPair>;
  /** 已登录状态下用动态码确认 MFA 绑定（204）。 */
  confirmMfaBinding(code: string): Promise<void>;
  /**
   * 初始化 TOTP 绑定；密钥与恢复码只在本次响应出现（§10.1）。
   * 传票据表示登录前强制绑定，不携带 Bearer；省略则使用当前会话。
   */
  setupMfa(mfaSetupTicket?: string): Promise<MFASetupData>;
  /** 解绑 MFA（需动态码或恢复码），成功后会撤销全部会话。 */
  disableMfa(code: string): Promise<void>;
  listSessions(): Promise<AuthSessionListData>;
  revokeSession(id: string): Promise<void>;
  revokeAllSessions(): Promise<void>;
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
    verifyMfaLogin: (mfaTicket, code) =>
      http.request<TokenPair>('/admin/v1/auth/mfa/verify', {
        method: 'POST',
        auth: false,
        body: { mfa_ticket: mfaTicket, code },
        schema: tokenPairSchema,
        retry: 'none',
      }),
    verifyMfaSetup: (mfaSetupTicket, code) =>
      http.request<TokenPair>('/admin/v1/auth/mfa/verify', {
        method: 'POST',
        // 登录前没有访问令牌：显式关闭 Bearer，避免残留会话把请求导向绑定确认分支。
        auth: false,
        body: { mfa_setup_ticket: mfaSetupTicket, code },
        schema: tokenPairSchema,
        retry: 'none',
      }),
    confirmMfaBinding: (code) =>
      http.requestVoid('/admin/v1/auth/mfa/verify', {
        method: 'POST',
        body: { code },
        retry: 'none',
      }),
    setupMfa: (mfaSetupTicket) =>
      http.request<MFASetupData>('/admin/v1/auth/mfa/setup', {
        method: 'POST',
        ...(mfaSetupTicket === undefined
          ? {}
          : { auth: false, body: { mfa_setup_ticket: mfaSetupTicket } }),
        schema: mfaSetupDataSchema,
        retry: 'none',
      }),
    disableMfa: (code) =>
      http.requestVoid('/admin/v1/auth/mfa/disable', {
        method: 'POST',
        body: { code },
        retry: 'none',
      }),
    listSessions: () =>
      http.request<AuthSessionListData>('/admin/v1/auth/sessions', {
        schema: authSessionListDataSchema,
        retry: 'safe',
      }),
    revokeSession: (id) =>
      http.requestVoid(`/admin/v1/auth/sessions/${encodeURIComponent(id)}`, {
        method: 'DELETE',
        retry: 'safe',
      }),
    revokeAllSessions: () =>
      http.requestVoid('/admin/v1/auth/sessions/revoke-all', { method: 'POST' }),
    logout: () => http.requestVoid('/admin/v1/auth/logout', { method: 'POST' }),
    logoutAll: () => http.requestVoid('/admin/v1/auth/logout-all', { method: 'POST' }),
  };
}
