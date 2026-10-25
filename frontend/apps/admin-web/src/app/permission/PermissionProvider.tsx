import { isAppError } from '@admin-frame/api-client';
import { createPermissionChecker } from '@admin-frame/auth';
import { useQuery } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, type ReactNode } from 'react';
import { useAuth } from '../auth/auth-context';
import {
  DENY_ALL,
  PermissionContextProvider,
  permissionSnapshotKeys,
  type PermissionState,
} from '../../entities/permission';

/**
 * 权限快照装配（§7.2、D-03）：登录后拉取，60s 内复用，40300 时强制刷新一次。
 * 页面不得自行请求权限接口；不可用时由 RequirePermission fail-closed。
 */
export function PermissionProvider({ children }: { children: ReactNode }) {
  const { client, status, permissionEpoch } = useAuth();
  const query = useQuery({
    queryKey: permissionSnapshotKeys.current,
    queryFn: () => client.iam.mePermissions(),
    enabled: status === 'authenticated',
    staleTime: 60_000,
    retry: false,
  });
  const { refetch } = query;

  useEffect(() => {
    if (permissionEpoch === 0 || status !== 'authenticated') {
      return;
    }
    void refetch();
  }, [permissionEpoch, status, refetch]);

  const refresh = useCallback(() => {
    void refetch();
  }, [refetch]);

  const value = useMemo<PermissionState>(() => {
    if (status !== 'authenticated') {
      return { status: 'idle', checker: DENY_ALL, refresh };
    }
    if (query.isError) {
      return {
        status: 'unavailable',
        checker: DENY_ALL,
        traceId: isAppError(query.error) ? query.error.traceId : undefined,
        refresh,
      };
    }
    if (query.data) {
      return {
        status: 'ready',
        checker: createPermissionChecker(query.data.permissions),
        snapshot: query.data,
        refresh,
      };
    }
    return { status: 'loading', checker: DENY_ALL, refresh };
  }, [status, query.isError, query.error, query.data, refresh]);

  return <PermissionContextProvider value={value}>{children}</PermissionContextProvider>;
}
