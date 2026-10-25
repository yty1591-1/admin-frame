import type { AppRuntimeConfig } from '@admin-frame/shared';
import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useState, type ReactNode } from 'react';
import { AuthContext, type AuthStatus } from './auth-context';
import { createAuthBootstrap } from './create-auth-bootstrap';

type AuthProviderProps = {
  config: AppRuntimeConfig;
  children: ReactNode;
};

/** 快照请求自身的 403 不再触发刷新，避免刷新循环（§7.2）。 */
const PERMISSION_SNAPSHOT_SUFFIX = '/iam/me/permissions';

/**
 * 登录态装配层：只负责创建 runtime、启动期恢复与登出后的缓存清理，
 * 不包含任何权限与页面逻辑（§5.1 app 层职责）。
 */
export function AuthProvider({ config, children }: AuthProviderProps) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<AuthStatus>('booting');
  const [permissionEpoch, setPermissionEpoch] = useState(0);
  const [bootstrap] = useState(() =>
    createAuthBootstrap(
      config,
      () => {
        queryClient.clear();
        setStatus('anonymous');
      },
      (path) => {
        if (!path.endsWith(PERMISSION_SNAPSHOT_SUFFIX)) {
          setPermissionEpoch((value) => value + 1);
        }
      },
    ),
  );
  const { runtime, client } = bootstrap;

  useEffect(() => {
    let cancelled = false;
    void runtime.restore().then((restored) => {
      if (!cancelled) {
        setStatus(restored ? 'authenticated' : 'anonymous');
      }
    });
    return () => {
      cancelled = true;
    };
  }, [runtime]);

  const endSession = async (action: () => Promise<void>): Promise<void> => {
    try {
      await action();
    } catch {
      // 接口失败也必须完成本地登出，避免残留令牌。
    } finally {
      queryClient.clear();
      setStatus('anonymous');
    }
  };

  return (
    <AuthContext.Provider
      value={{
        status,
        runtime,
        client,
        permissionEpoch,
        markAuthenticated: () => {
          setStatus('authenticated');
        },
        signOut: () => endSession(() => runtime.logout()),
        signOutEverywhere: () => endSession(() => runtime.logoutAll()),
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}
