import type { SubjectPermissions } from '@admin-frame/api-client';
import type { PermissionChecker } from '@admin-frame/auth';
import { createPermissionChecker } from '@admin-frame/auth';
import { defaultTranslator, useI18n } from '@admin-frame/i18n';
import { ErrorState, PageLoading } from '@admin-frame/ui';
import { createContext, useContext, type ReactNode } from 'react';

export type PermissionRequirement = string | readonly string[] | undefined;

export type PermissionState =
  | { status: 'idle' | 'loading'; checker: PermissionChecker; refresh: () => void }
  | {
      status: 'ready';
      checker: PermissionChecker;
      snapshot: SubjectPermissions;
      refresh: () => void;
    }
  | {
      status: 'unavailable';
      checker: PermissionChecker;
      traceId: string | undefined;
      refresh: () => void;
    };

const DENY_ALL = createPermissionChecker();

const PermissionContext = createContext<PermissionState | null>(null);

export function PermissionContextProvider({
  value,
  children,
}: {
  value: PermissionState;
  children: ReactNode;
}) {
  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
}

/** 权限状态只经此入口消费；任何组件不得自行请求权限快照（§7.2）。 */
export function usePermissionState(): PermissionState {
  const value = useContext(PermissionContext);
  if (!value) {
    throw new Error(
      defaultTranslator('error.dev.missingProvider', {
        hook: 'usePermissionState',
        provider: 'PermissionContextProvider',
      }),
    );
  }
  return value;
}

/** 空需求表示仅需登录；多码为“任一满足”（§7.1）。 */
export function checkPermission(
  checker: PermissionChecker,
  requirement: PermissionRequirement,
): boolean {
  if (requirement === undefined) {
    return true;
  }
  if (typeof requirement === 'string') {
    return checker.has(requirement);
  }
  return requirement.length === 0 ? true : checker.hasAny(requirement);
}

export function usePermissionChecker(): PermissionChecker {
  return usePermissionState().checker;
}

/** 按钮/区块可见性判定；快照未就绪时默认拒绝（§7.3）。 */
export function usePermission(requirement: PermissionRequirement): boolean {
  const state = usePermissionState();
  return state.status === 'ready' && checkPermission(state.checker, requirement);
}

export function Can({
  permission,
  fallback = null,
  children,
}: {
  permission: PermissionRequirement;
  fallback?: ReactNode;
  children: ReactNode;
}) {
  const allowed = usePermission(permission);
  return <>{allowed ? children : fallback}</>;
}

/**
 * 路由权限守卫（§7.2）：
 * 快照未就绪 → 等待；接口不可用 → fail-closed；无权限 → 原地渲染 403 并保留导航上下文。
 */
export function RequirePermission({
  permission,
  children,
}: {
  permission: PermissionRequirement;
  children: ReactNode;
}) {
  const state = usePermissionState();
  const { t } = useI18n();

  if (state.status === 'unavailable') {
    return (
      <ErrorState
        title={t('permission.state.unavailableTitle')}
        description={t('permission.state.unavailableDescription')}
        traceId={state.traceId}
        onRetry={state.refresh}
      />
    );
  }
  if (state.status !== 'ready') {
    return <PageLoading tip={t('permission.state.loading')} />;
  }
  if (!checkPermission(state.checker, permission)) {
    return (
      <ErrorState
        title={t('permission.state.deniedTitle')}
        description={t('permission.state.deniedDescription')}
      />
    );
  }
  return <>{children}</>;
}

export { DENY_ALL };
