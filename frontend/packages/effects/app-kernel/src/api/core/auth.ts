import type {
  AxiosResponse,
  RequestClient,
  RequestClientConfig,
} from '@vben/request';

import type { KernelCapabilities } from '../../types';

import { KernelCapabilityError } from '../../types';

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

/** 页面/store 只消费该视图，DTO 映射集中在内核。 */
export interface TokenPairVM {
  accessToken: string;
  refreshToken: string;
}

/**
 * 登录结果四态（api/openapi/paths/admin/auth.yaml#admin-v1-auth-login）。
 *
 * - token：直接拿到令牌对；
 * - mfa-challenge：已完成密码校验，需再用 mfa_ticket 完成第二步；
 * - mfa-binding：A 端强制绑定，需用 mfa_setup_ticket 完成绑定后才会签发令牌；
 * - password-change：管理员重置密码后必须先凭 password_change_ticket 完成改密。
 */
export type LoginOutcome =
  | { kind: 'mfa-binding'; ticket: string }
  | { kind: 'mfa-challenge'; ticket: string }
  | { kind: 'password-change'; ticket: string }
  | { kind: 'token'; tokens: TokenPairVM };

/** MFA 绑定初始化数据；密钥与恢复码只在本次响应出现。 */
export interface MfaSetupData {
  otpauth_uri: string;
  recovery_codes: string[];
  secret: string;
}

/** 当前主体权限快照（api/openapi/schemas/admin/iam-bindings.yaml#SubjectPermissions）。 */
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

/** 令牌 DTO → 页面模型；页面/store 不再接触 snake_case。 */
export function toTokenPairVM(dto: TokenPairDTO): TokenPairVM {
  return {
    accessToken: dto.access_token,
    refreshToken: dto.refresh_token,
  };
}

/** 登录 / 改密响应的中间态载荷；成功时即 TokenPair。 */
interface LoginPayload extends Partial<TokenPairDTO> {
  mfa_binding_required?: boolean;
  mfa_required?: boolean;
  mfa_setup_ticket?: string;
  mfa_ticket?: string;
  password_change_required?: boolean;
  password_change_ticket?: string;
}

/** 设备会话（不改名、不带令牌/IP/UA 原文）。 */
export interface AuthSessionDTO {
  absolute_expires_at: string;
  browser?: 'chrome' | 'edge' | 'firefox' | 'opera' | 'safari' | 'wechat';
  created_at: string;
  current: boolean;
  device_label?: string;
  device_type?: 'desktop' | 'mobile' | 'tablet';
  expires_at: string;
  id: string;
  idle_expires_at?: string;
  ip_masked?: string;
  last_used_at: string;
  os?: 'android' | 'chromeos' | 'ios' | 'linux' | 'macos' | 'windows';
  revoked_at?: string;
  status: 'active' | 'expired' | 'revoked' | 'rotated';
}

/** 设备会话分页结果。 */
export interface AuthSessionListDTO {
  items: AuthSessionDTO[];
  next_cursor?: string;
}

export interface CreateAuthApiOptions {
  baseRequestClient: RequestClient;
  /** 能力开关：关闭的中间态（MFA/改密）收到服务端要求时直接抛错。 */
  capabilities: KernelCapabilities;
  requestClient: RequestClient;
}

/**
 * 构建认证 API。
 *
 * A/B 端的差异收敛为能力开关：同一套方法，B 端关闭 MFA/改密，
 * 服务端若返回中间态载荷即 fail-closed，不静默当成功。
 */
export function createAuthApi({
  baseRequestClient,
  capabilities,
  requestClient,
}: CreateAuthApiOptions) {
  function requireCapability(capability: keyof KernelCapabilities) {
    if (!capabilities[capability]) {
      throw new KernelCapabilityError(capability);
    }
  }

  function parseLoginPayload(payload: LoginPayload): LoginOutcome {
    if (payload.password_change_required) {
      requireCapability('passwordChange');
      return {
        kind: 'password-change',
        ticket: payload.password_change_ticket ?? '',
      };
    }
    if (payload.mfa_binding_required) {
      requireCapability('mfaBinding');
      return { kind: 'mfa-binding', ticket: payload.mfa_setup_ticket ?? '' };
    }
    if (payload.mfa_required) {
      requireCapability('mfa');
      return { kind: 'mfa-challenge', ticket: payload.mfa_ticket ?? '' };
    }
    return { kind: 'token', tokens: toTokenPairVM(payload as TokenPairDTO) };
  }

  /** 密码登录；入参仍使用 UI 的 username，出参映射为契约要求的 subject。 */
  async function loginApi(data: {
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
    return parseLoginPayload(envelope.data ?? {});
  }

  /** 强制改密（一次性票据，不携带 Authorization）。 */
  async function completePasswordChangeApi(params: {
    newPassword: string;
    ticket: string;
  }): Promise<LoginOutcome> {
    requireCapability('passwordChange');
    const res = await baseRequestClient.post<
      AxiosResponse<Envelope<LoginPayload>>
    >('/auth/password/change', {
      new_password: params.newPassword,
      password_change_ticket: params.ticket,
    });
    const envelope = res.data;
    if (envelope.code !== 0) {
      throw new Error(envelope.message || 'password change failed');
    }
    return parseLoginPayload(envelope.data ?? {});
  }

  /**
   * 自助改密（Bearer）：旧密码错误返回 40100，必须绕开 requestClient 的 401
   * 自动刷新，否则输错密码会演变成登出；成功返回 204，用 raw。
   */
  async function changePasswordApi(params: {
    accessToken?: null | string;
    newPassword: string;
    oldPassword: string;
  }): Promise<void> {
    await baseRequestClient.post(
      '/auth/password/change',
      { new_password: params.newPassword, old_password: params.oldPassword },
      {
        headers: params.accessToken
          ? { Authorization: `Bearer ${params.accessToken}` }
          : {},
        responseReturn: 'raw',
      },
    );
  }

  /** 刷新令牌（一次性轮换）。 */
  async function refreshTokenApi(refreshToken: string): Promise<TokenPairVM> {
    const res = await baseRequestClient.post<
      AxiosResponse<Envelope<TokenPairDTO>>
    >('/auth/refresh', { refresh_token: refreshToken });
    if (res.data.code !== 0 || !res.data.data) {
      throw new Error(res.data.message || 'refresh failed');
    }
    return toTokenPairVM(res.data.data);
  }

  /** 登出当前会话（204 raw；显式 Bearer，避免与刷新递归）。 */
  async function logoutApi(accessToken?: null | string): Promise<void> {
    await baseRequestClient.post('/auth/logout', undefined, {
      headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
      responseReturn: 'raw',
    });
  }

  /** 初始化（或重新初始化）TOTP 绑定；登录前强制绑定时传入 setup ticket。 */
  async function mfaSetupApi(setupTicket: string): Promise<MfaSetupData> {
    requireCapability('mfaBinding');
    const res = await baseRequestClient.post<
      AxiosResponse<Envelope<MfaSetupData>>
    >('/auth/mfa/setup', { mfa_setup_ticket: setupTicket });
    const envelope = res.data;
    if (envelope.code !== 0 || !envelope.data) {
      throw new Error(envelope.message || 'mfa setup failed');
    }
    return envelope.data;
  }

  /** 完成 MFA 校验（两步登录传 mfaTicket，强制绑定传 mfaSetupTicket）。 */
  async function mfaVerifyApi(params: {
    code: string;
    mfaSetupTicket?: string;
    mfaTicket?: string;
  }): Promise<TokenPairVM> {
    if (params.mfaTicket) {
      requireCapability('mfa');
    } else if (params.mfaSetupTicket) {
      requireCapability('mfaBinding');
    }
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

  /** 已登录账号初始化 TOTP 绑定；已确认绑定时返回 40900。 */
  async function mfaSetupSessionApi(): Promise<MfaSetupData> {
    requireCapability('mfaBinding');
    return await requestClient.post<MfaSetupData>('/auth/mfa/setup', {});
  }

  /** 已登录账号用动态码确认首次绑定（40100 必须绕开自动刷新，204 raw）。 */
  async function confirmMfaBindingSessionApi(
    code: string,
    accessToken?: null | string,
  ): Promise<void> {
    requireCapability('mfaBinding');
    await baseRequestClient.post(
      '/auth/mfa/verify',
      { code },
      {
        headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
        responseReturn: 'raw',
      },
    );
  }

  /** 解绑 MFA；成功后服务端撤销全部会话。 */
  async function disableMfaApi(
    code: string,
    accessToken?: null | string,
  ): Promise<void> {
    requireCapability('mfa');
    await baseRequestClient.post(
      '/auth/mfa/disable',
      { code },
      {
        headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
        responseReturn: 'raw',
      },
    );
  }

  /** 当前主体的角色与权限快照。 */
  async function getMyPermissionsApi(): Promise<SubjectPermissions> {
    return requestClient.get<SubjectPermissions>('/iam/me/permissions');
  }

  /** 仅返回权限码。 */
  async function getAccessCodesApi(): Promise<string[]> {
    const permissions = await getMyPermissionsApi();
    return permissions.permissions;
  }

  /** 列出本账号的设备会话。 */
  async function listMySessionsApi(
    params: { cursor?: string; limit?: number } = {},
    config?: Omit<RequestClientConfig, 'params'>,
  ): Promise<AuthSessionListDTO> {
    return requestClient.get<AuthSessionListDTO>('/auth/sessions', {
      ...config,
      params,
    });
  }

  /** 撤销指定设备会话。 */
  async function revokeMySessionApi(
    id: string,
    config?: RequestClientConfig,
  ): Promise<void> {
    await requestClient.delete(`/auth/sessions/${encodeURIComponent(id)}`, {
      ...config,
      responseReturn: 'raw',
    });
  }

  /** 撤销全部会话（语义等同 logout-all）。 */
  async function revokeAllMySessionsApi(
    config?: RequestClientConfig,
  ): Promise<void> {
    await requestClient.post('/auth/sessions/revoke-all', undefined, {
      ...config,
      responseReturn: 'raw',
    });
  }

  return {
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
  };
}
