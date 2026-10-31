import type { RequestClientConfig } from '@vben/request';
import type { UserInfo } from '@vben/types';

import { preferences } from '@vben/preferences';

import { requestClient } from '#/api/request';

/** GET /admin/v1/me 的响应（api/openapi/schemas/admin/me.yaml#MeView）。 */
export interface MeView {
  channel: 'admin' | 'biz';
  /**
   * 展示昵称；服务端始终返回，空串表示未设置。
   *
   * 前端统一用 `display_name || subject` 展示，避免出现空白身份卡。
   */
  display_name: string;
  /** 是否已设置密码凭据。 */
  has_password: boolean;
  merchant_id: string;
  mfa_enabled: boolean;
  /** 管理员重置密码后尚未改密时为 true。 */
  must_change_password: boolean;
  /** 最近一次改密时间；从未改密时后端省略该字段。 */
  password_updated_at?: string;
  /** 是否已绑定手机凭据；号码本身不可回显。 */
  phone_bound: boolean;
  status: 'active' | 'disabled' | 'locked' | 'pending';
  subject: string;
  subject_id: string;
  token_version: number;
}

/**
 * 获取当前登录主体资料。
 *
 * 只含身份域字段；角色与权限由 /iam/me/permissions 提供。
 */
export async function getMeApi(config?: RequestClientConfig): Promise<MeView> {
  return requestClient.get<MeView>('/me', config);
}

/**
 * 更新当前登录主体资料（`PATCH /admin/v1/me`）。
 *
 * 仅支持展示昵称：传空串表示清除，服务端返回更新后的完整资料，
 * 调用方应直接用返回值刷新本地用户信息，避免再发一次 GET /me。
 */
export async function updateMyProfileApi(
  data: { display_name: string },
  config?: RequestClientConfig,
): Promise<MeView> {
  return requestClient.request<MeView>('/me', {
    ...config,
    data,
    method: 'PATCH',
  });
}

/**
 * 获取用户信息。
 *
 * 昵称为空时回退登录名；头像固定空串，让 `VbenAvatar` 用昵称首字母兜底，
 * 从而不再依赖 `preferences.app.defaultAvatar` 的远端 unpkg 资源（FEREV-02）。
 */
export async function getUserInfoApi(): Promise<UserInfo> {
  const me = await getMeApi();
  const displayName = me.display_name || me.subject;
  return {
    avatar: '',
    desc: '',
    homePath: preferences.app.defaultHomePath,
    realName: displayName,
    roles: [],
    token: '',
    userId: me.subject_id,
    username: me.subject,
  };
}
