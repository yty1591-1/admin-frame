import { useI18n } from '@admin-frame/i18n';
import { PageHeader } from '@admin-frame/ui';
import { useAuth } from '../app/auth/auth-context';
import { PermissionCatalogPanel } from '../features/permission-catalog';

export default function PermissionCatalogPage() {
  const { client } = useAuth();
  const { t } = useI18n();

  return (
    <>
      <PageHeader
        title={t('permission.catalog.title')}
        description={t('permission.catalog.description')}
      />
      <PermissionCatalogPanel api={client.iam} />
    </>
  );
}
