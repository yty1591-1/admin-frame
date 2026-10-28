import type { AuthorizePayload, AuthorizeResult } from './types';

import { requestClient } from '#/api/request';

/** 模拟鉴权：不落地授权，返回判定原因、命中角色与数据范围。 */
export async function simulateAuthorizeApi(
  payload: AuthorizePayload,
): Promise<AuthorizeResult> {
  return requestClient.post<AuthorizeResult>('/iam/authorize', payload);
}
