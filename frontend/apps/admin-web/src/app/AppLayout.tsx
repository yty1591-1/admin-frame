import { useI18n } from '@admin-frame/i18n';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { AppShell, Button } from '@admin-frame/ui';
import { useMemo } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router';
import { usePermissionState } from '../entities/permission';
import { useAuth } from './auth/auth-context';
import { RouteErrorBoundary } from './RouteErrorBoundary';
import { buildMenuEntries } from './routes';

export function AppLayout({ config }: { config: AppRuntimeConfig }) {
  const navigate = useNavigate();
  const location = useLocation();
  const { t } = useI18n();
  const { signOut } = useAuth();
  const { status, checker } = usePermissionState();
  // 快照未就绪时菜单为空，避免展示不可用的入口（§7.4）。
  const menus = useMemo(
    () => (status === 'ready' ? buildMenuEntries(checker, t) : []),
    [status, checker, t],
  );

  return (
    <AppShell
      title={t('common.appNameAdmin')}
      subtitle={t('common.envLabel', { env: config.env })}
      menus={menus}
      selectedKey={location.pathname}
      onSelect={(key) => {
        void navigate(key);
      }}
      actions={
        <Button
          onClick={() => {
            void signOut();
          }}
        >
          {t('common.logout')}
        </Button>
      }
    >
      <RouteErrorBoundary>
        <Outlet />
      </RouteErrorBoundary>
    </AppShell>
  );
}
