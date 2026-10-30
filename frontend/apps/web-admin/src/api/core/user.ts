import type { RequestClientConfig } from '@vben/request';
import type { UserInfo } from '@vben/types';

import { preferences } from '@vben/preferences';

import { requestClient } from '#/api/request';

/** GET /admin/v1/me 的响应（api/openapi/schemas/admin/me.yaml#MeView）。 */
export interface MeView {
  channel: 'admin' | 'biz';
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
 * 获取用户信息。
 *
 * identity_account 暂无 display_name / avatar_url，昵称回退 subject、头像用默认值。
 */
export async function getUserInfoApi(): Promise<UserInfo> {
  const me = await getMeApi();
  return {
    avatar: preferences.app.defaultAvatar,
    desc: '',
    homePath: preferences.app.defaultHomePath,
    realName: me.subject,
    roles: [],
    token: '',
    userId: me.subject_id,
    username: me.subject,
  };
}
