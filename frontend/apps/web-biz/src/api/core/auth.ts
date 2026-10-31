import type { AxiosResponse } from '@vben/request';

import { baseRequestClient, requestClient } from '#/api/request';

/**
 * B 端认证接口（admin-frame 契约）。
 *
 * | 前端函数        | admin-frame 契约                        |
 * |-----------------|-----------------------------------------|
 * | loginApi        | POST /biz/v1/auth/login                 |
 * | refreshTokenApi | POST /biz/v1/auth/refresh               |
 * | logoutApi       | POST /biz/v1/auth/logout（Bearer，204） |
 * | getAccessCodesApi | GET /biz/v1/iam/me/permissions        |
 *
 * 与 A 端不同，B 端登录没有 MFA / 强制改密中间态，成功即返回令牌对；
 * 因此这里不需要四态解析，DTO 到页面模型的映射集中在本文件。
 */

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
  expires_in: number;
  refresh_token: string;
  token_type: 'Bearer';
}

/** 页面/store 只消费该视图，避免 snake_case 扩散。 */
export interface TokenPairVM {
  accessToken: string;
  refreshToken: string;
}

/** 当前主体权限快照（api/openapi/schemas/biz/iam-permissions.yaml）。 */
export interface SubjectPermissions {
  channel: 'admin' | 'biz';
  data_scopes: unknown[];
  generated_at: string;
  merchant_id?: string;
  permissions: string[];
  roles: string[];
  snapshot_version: number;
  subject_id: string;
  subject_type: string;
}

/** 令牌 DTO → 页面模型。 */
export function toTokenPairVM(dto: TokenPairDTO): TokenPairVM {
  return {
    accessToken: dto.access_token,
    refreshToken: dto.refresh_token,
  };
}

/**
 * 密码登录。
 *
 * 入参沿用 UI 的 username，出参映射为契约要求的 subject；
 * 服务端 channel / merchant_id 均由账号推导，传了会被拒绝（40000）。
 */
export async function loginApi(data: {
  password: string;
  username: string;
}): Promise<TokenPairVM> {
  const res = await baseRequestClient.post<
    AxiosResponse<Envelope<TokenPairDTO>>
  >('/auth/login', { password: data.password, subject: data.username });
  const envelope = res.data;
  if (envelope.code !== 0 || !envelope.data) {
    throw new Error(envelope.message || 'login failed');
  }
  return toTokenPairVM(envelope.data);
}

/**
 * 刷新令牌（一次性轮换）。
 *
 * 成功后旧 refresh token 立即失效，重放会撤销整条会话链，
 * 因此调用方必须在同一次赋值内替换两个令牌。
 */
export async function refreshTokenApi(
  refreshToken: string,
): Promise<TokenPairVM> {
  const res = await baseRequestClient.post<
    AxiosResponse<Envelope<TokenPairDTO>>
  >('/auth/refresh', { refresh_token: refreshToken });
  const envelope = res.data;
  if (envelope.code !== 0 || !envelope.data) {
    throw new Error(envelope.message || 'refresh failed');
  }
  return toTokenPairVM(envelope.data);
}

/**
 * 登出当前会话。
 *
 * 后端返回 204 无 body，必须用 raw 避免被信封拦截器判为失败；
 * 使用无拦截器的 baseRequestClient 并显式带 Bearer：登出时令牌可能已过期，
 * 若走 requestClient 会触发刷新/重认证，进而与 authStore.logout 形成递归。
 */
export async function logoutApi(accessToken?: null | string): Promise<void> {
  await baseRequestClient.post('/auth/logout', undefined, {
    headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
    responseReturn: 'raw',
  });
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
