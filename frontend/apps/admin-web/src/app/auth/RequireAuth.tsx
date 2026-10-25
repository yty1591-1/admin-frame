import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router';
import { PageLoading } from '@admin-frame/ui';
import { useAuth } from './auth-context';

/** 登录守卫只负责会话，不承担权限判定（§7.2）。 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { status } = useAuth();
  const location = useLocation();

  if (status === 'booting') {
    return <PageLoading />;
  }
  if (status === 'anonymous') {
    const returnTo = encodeURIComponent(`${location.pathname}${location.search}`);
    return <Navigate to={`/login?returnTo=${returnTo}`} replace />;
  }
  return <>{children}</>;
}
