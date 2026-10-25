import { Outlet, useLocation, useNavigate } from 'react-router';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { AppShell, type AppShellMenuEntry } from '@admin-frame/ui';

const ADMIN_MENUS: readonly AppShellMenuEntry[] = [{ key: '/', label: '工作台' }];

export function AppLayout({ config }: { config: AppRuntimeConfig }) {
  const navigate = useNavigate();
  const location = useLocation();

  return (
    <AppShell
      title="A 端运营后台"
      subtitle={`环境：${config.env}`}
      menus={ADMIN_MENUS}
      selectedKey={location.pathname}
      onSelect={(key) => {
        void navigate(key);
      }}
    >
      <Outlet />
    </AppShell>
  );
}
