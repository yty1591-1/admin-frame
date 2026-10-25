import type { AdminApiClient } from '@admin-frame/api-client';
import type { SessionRuntime } from '@admin-frame/auth';
import { createContext, useContext } from 'react';

export type AuthStatus = 'booting' | 'anonymous' | 'authenticated';

export type AuthContextValue = {
  status: AuthStatus;
  runtime: SessionRuntime;
  /** 已接线的 A 端客户端；业务数据层只通过它请求接口（FE-06）。 */
  client: AdminApiClient;
  /** 40300 计数：变化即强制刷新权限快照（§7.2）。 */
  permissionEpoch: number;
  markAuthenticated: () => void;
  signOut: () => Promise<void>;
  signOutEverywhere: () => Promise<void>;
};

export const AuthContext = createContext<AuthContextValue | null>(null);

/** 登录态只经此入口消费，避免组件自行读取令牌（FE-07）。 */
export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext);
  if (!value) {
    throw new Error('useAuth 必须在 AuthProvider 内使用');
  }
  return value;
}
