import type { UserInfo } from '@vben/types';

import { preferences } from '@vben/preferences';

import { requestClient } from '#/api/request';

/** GET /admin/v1/me 的响应（api/openapi/components.yaml#MeView）。 */
export interface MeView {
  channel: 'admin' | 'biz';
  merchant_id: string;
  mfa_enabled: boolean;
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
export async function getMeApi(): Promise<MeView> {
  return requestClient.get<MeView>('/me');
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
