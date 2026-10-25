import type { AppRuntimeConfig } from '@admin-frame/shared';
import { AppShell, Button } from '@admin-frame/ui';
import { useMemo } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router';
import { usePermissionState } from '../entities/permission';
import { useAuth } from './auth/auth-context';
import { buildMenuEntries } from './routes';

export function AppLayout({ config }: { config: AppRuntimeConfig }) {
  const navigate = useNavigate();
  const location = useLocation();
  const { signOut } = useAuth();
  const { status, checker } = usePermissionState();
  // 快照未就绪时菜单为空，避免展示不可用的入口（§7.4）。
  const menus = useMemo(
    () => (status === 'ready' ? buildMenuEntries(checker) : []),
    [status, checker],
  );

  return (
    <AppShell
      title="A 端运营后台"
      subtitle={`环境：${config.env}`}
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
          退出登录
        </Button>
      }
    >
      <Outlet />
    </AppShell>
  );
}
