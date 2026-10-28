import type { AxiosResponse } from '@vben/request';

import { baseRequestClient, requestClient } from '#/api/request';

/** 后端统一信封（api/openapi/components.yaml#Envelope）。 */
interface Envelope<T> {
  code: number;
  data: T;
  message: string;
  trace_id?: string;
}

/** 契约中的令牌对（snake_case）。 */
export interface TokenPairDTO {
  access_token: string;
  refresh_token: string;
  token_type: 'Bearer';
  expires_in: number;
}

/** 页面/store 只消费该视图，DTO 映射集中在本文件。 */
export interface TokenPairVM {
  accessToken: string;
  refreshToken: string;
}

/**
 * 登录结果三态（api/openapi/paths/admin/auth.yaml#admin-v1-auth-login）。
 *
 * - token：直接拿到令牌对；
 * - mfa-challenge：已完成密码校验，需再用 mfa_ticket 完成第二步；
 * - mfa-binding：A 端强制绑定，需用 mfa_setup_ticket 完成绑定后才会签发令牌。
 */
export type LoginOutcome =
  | { kind: 'mfa-binding'; ticket: string }
  | { kind: 'mfa-challenge'; ticket: string }
  | { kind: 'token'; tokens: TokenPairVM };

/** MFA 绑定初始化数据；密钥与恢复码只在本次响应出现。 */
export interface MfaSetupData {
  otpauth_uri: string;
  recovery_codes: string[];
  secret: string;
}

/** 当前主体权限快照（api/openapi/schemas/admin/iam-bindings.yaml#SubjectPermissions）。 */
export interface SubjectPermissions {
  channel: 'admin';
  data_scopes: unknown[];
  generated_at: string;
  merchant_id?: string;
  permissions: string[];
  roles: string[];
  snapshot_version: number;
  subject_id: string;
  subject_type: string;
}

/** 令牌 DTO → 页面模型；页面/store 不再接触 snake_case。 */
export function toTokenPairVM(dto: TokenPairDTO): TokenPairVM {
  return {
    accessToken: dto.access_token,
    refreshToken: dto.refresh_token,
  };
}

/** 登录响应的三态载荷；成功时即 TokenPair。 */
interface LoginPayload extends Partial<TokenPairDTO> {
  mfa_binding_required?: boolean;
  mfa_required?: boolean;
  mfa_setup_ticket?: string;
  mfa_ticket?: string;
}

/**
 * 密码登录。
 *
 * 入参仍使用 UI 的 username，出参映射为契约要求的 subject。
 */
export async function loginApi(data: {
  password: string;
  username: string;
}): Promise<LoginOutcome> {
  const res = await baseRequestClient.post<
    AxiosResponse<Envelope<LoginPayload>>
  >('/auth/login', { password: data.password, subject: data.username });
  const envelope = res.data;
  if (envelope.code !== 0) {
    throw new Error(envelope.message || 'login failed');
  }
  const payload = envelope.data ?? {};
  if (payload.mfa_binding_required) {
    return { kind: 'mfa-binding', ticket: payload.mfa_setup_ticket ?? '' };
  }
  if (payload.mfa_required) {
    return { kind: 'mfa-challenge', ticket: payload.mfa_ticket ?? '' };
  }
  return { kind: 'token', tokens: toTokenPairVM(payload as TokenPairDTO) };
}

/**
 * 刷新令牌（一次性轮换）。
 *
 * 成功后旧 refresh token 立即失效，调用方必须同时替换 access / refresh。
 */
export async function refreshTokenApi(
  refreshToken: string,
): Promise<TokenPairVM> {
  const res = await baseRequestClient.post<
    AxiosResponse<Envelope<TokenPairDTO>>
  >('/auth/refresh', { refresh_token: refreshToken });
  if (res.data.code !== 0 || !res.data.data) {
    throw new Error(res.data.message || 'refresh failed');
  }
  return toTokenPairVM(res.data.data);
}

/**
 * 登出当前会话。
 *
 * 后端返回 204 无 body，必须用 raw 避免被信封拦截器判为失败。
 * 使用无拦截器的 baseRequestClient 并显式带 Bearer：登出时令牌可能已过期，
 * 若走 requestClient 会触发刷新/重认证，进而与 authStore.logout 形成递归。
 */
export async function logoutApi(accessToken?: null | string): Promise<void> {
  await baseRequestClient.post('/auth/logout', undefined, {
    headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
    responseReturn: 'raw',
  });
}

/** 初始化（或重新初始化）TOTP 绑定；登录前强制绑定时传入 setup ticket。 */
export async function mfaSetupApi(setupTicket: string): Promise<MfaSetupData> {
  const res = await baseRequestClient.post<
    AxiosResponse<Envelope<MfaSetupData>>
  >('/auth/mfa/setup', { mfa_setup_ticket: setupTicket });
  const envelope = res.data;
  if (envelope.code !== 0 || !envelope.data) {
    throw new Error(envelope.message || 'mfa setup failed');
  }
  return envelope.data;
}

/**
 * 完成 MFA 校验。
 *
 * 两步登录传 mfaTicket，强制绑定传 mfaSetupTicket；票据均一次性，
 * 校验失败（含动态码错误）即被消费。
 */
export async function mfaVerifyApi(params: {
  code: string;
  mfaSetupTicket?: string;
  mfaTicket?: string;
}): Promise<TokenPairVM> {
  const body: Record<string, string> = { code: params.code };
  if (params.mfaTicket) {
    body.mfa_ticket = params.mfaTicket;
  }
  if (params.mfaSetupTicket) {
    body.mfa_setup_ticket = params.mfaSetupTicket;
  }
  const res = await baseRequestClient.post<
    AxiosResponse<Envelope<TokenPairDTO>>
  >('/auth/mfa/verify', body);
  const envelope = res.data;
  if (envelope.code !== 0 || !envelope.data) {
    throw new Error(envelope.message || 'mfa verify failed');
  }
  return toTokenPairVM(envelope.data);
}

/** 当前主体的角色与权限快照。 */
export async function getMyPermissionsApi(): Promise<SubjectPermissions> {
  return requestClient.get<SubjectPermissions>('/iam/me/permissions');
}

/** 仅返回权限码，供只需要 accessCodes 的调用方使用。 */
export async function getAccessCodesApi(): Promise<string[]> {
  const permissions = await getMyPermissionsApi();
  return permissions.permissions;
}
