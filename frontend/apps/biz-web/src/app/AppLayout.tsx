import { useI18n } from '@admin-frame/i18n';
import { Outlet, useLocation, useNavigate } from 'react-router';
import type { AppRuntimeConfig } from '@admin-frame/shared';
import { AppShell } from '@admin-frame/ui';

const BIZ_MENU_KEYS = [{ key: '/', titleKey: 'nav.bizHome' }] as const;

export function AppLayout({ config }: { config: AppRuntimeConfig }) {
  const navigate = useNavigate();
  const location = useLocation();
  const { t } = useI18n();
  const menus = BIZ_MENU_KEYS.map((entry) => ({ key: entry.key, label: t(entry.titleKey) }));

  return (
    <AppShell
      title={t('common.appNameBiz')}
      subtitle={t('common.envLabel', { env: config.env })}
      menus={menus}
      selectedKey={location.pathname}
      onSelect={(key) => {
        void navigate(key);
      }}
    >
      <Outlet />
    </AppShell>
  );
}
