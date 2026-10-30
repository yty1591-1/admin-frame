import type { RequestClientConfig } from '@vben/request';
import type { UserInfo } from '@vben/types';

import { requestClient } from '#/api/request';

/**
 * GET /biz/v1/me 的响应（api/openapi/schemas/biz/me.yaml#MeView）。
 *
 * 与 A 端 `/admin/v1/me` 字段一致；`merchant_id` 恒等于令牌内商户。
 */
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
 * B 端认证尚未迁移到真实契约（FEREV-04），mock 环境下该端点不存在，
 * 调用方须按「不可用」降级，不得把失败当成「未绑定」。
 */
export async function getMeApi(config?: RequestClientConfig): Promise<MeView> {
  return requestClient.get<MeView>('/me', config);
}

/**
 * 获取用户信息
 */
export async function getUserInfoApi() {
  return requestClient.get<UserInfo>('/user/info');
}
