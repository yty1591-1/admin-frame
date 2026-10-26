import { createPermissionChecker } from '@admin-frame/auth';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { subjectPermissionsFixture, TEST_API_BASE_URL } from '@admin-frame/testing';
import { useState, type ReactNode } from 'react';
import { AuthContext, type AuthContextValue, type AuthStatus } from '../app/auth/auth-context';
import { createAuthBootstrap } from '../app/auth/create-auth-bootstrap';
import { DENY_ALL, PermissionContextProvider, type PermissionState } from '../entities/permission';

/** 测试运行期配置：apiBaseUrl 必须与 MSW handler 的 baseUrl 一致。 */
export const TEST_RUNTIME_CONFIG: AppRuntimeConfig = {
  apiBaseUrl: TEST_API_BASE_URL,
  env: 'local',
  channel: 'admin',
  features: {},
  monitoring: { dsn: null, sampleRate: 0, release: null },
};

type AuthHarnessProps = {
  children: ReactNode;
  /** 直接指定起始登录态，避免测试等待启动期恢复。 */
  initialStatus?: AuthStatus;
};

/**
 * 登录态测试外壳：使用真实 `SessionRuntime`（memory 令牌模式）与 HTTP 管线，
 * 请求由 MSW 拦截；仅替换启动期恢复流程。
 */
export function AuthHarness({ children, initialStatus = 'anonymous' }: AuthHarnessProps) {
  const [status, setStatus] = useState<AuthStatus>(initialStatus);
  const [bootstrap] = useState(() =>
    createAuthBootstrap(
      TEST_RUNTIME_CONFIG,
      () => {
        setStatus('anonymous');
      },
      undefined,
      { tokenStorageMode: 'memory' },
    ),
  );

  const value: AuthContextValue = {
    status,
    runtime: bootstrap.runtime,
    client: bootstrap.client,
    permissionEpoch: 0,
    markAuthenticated: () => {
      setStatus('authenticated');
    },
    signOut: () => {
      setStatus('anonymous');
      return Promise.resolve();
    },
    signOutEverywhere: () => {
      setStatus('anonymous');
      return Promise.resolve();
    },
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

type PermissionHarnessProps = {
  children: ReactNode;
  /** 缺省为空权限（仅需登录的路由/页面可渲染）。 */
  permissions?: readonly string[];
  status?: 'ready' | 'unavailable' | 'loading';
};

/** 权限快照测试外壳：直接注入状态，避免每个用例都走一遍快照请求。 */
export function PermissionHarness({
  children,
  permissions = [],
  status = 'ready',
}: PermissionHarnessProps) {
  const value: PermissionState =
    status === 'ready'
      ? {
          status: 'ready',
          checker: createPermissionChecker(permissions),
          snapshot: { ...subjectPermissionsFixture, permissions: [...permissions] },
          refresh: () => undefined,
        }
      : status === 'unavailable'
        ? {
            status: 'unavailable',
            checker: DENY_ALL,
            traceId: 'test-trace',
            refresh: () => undefined,
          }
        : { status: 'loading', checker: DENY_ALL, refresh: () => undefined };

  return <PermissionContextProvider value={value}>{children}</PermissionContextProvider>;
}
