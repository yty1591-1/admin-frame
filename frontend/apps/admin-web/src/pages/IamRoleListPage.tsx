import { useI18n } from '@admin-frame/i18n';
import { PageHeader } from '@admin-frame/ui';
import { useAuth } from '../app/auth/auth-context';
import { RoleListPanel } from '../features/iam-roles';

export default function IamRoleListPage() {
  const { client } = useAuth();
  const { t } = useI18n();

  return (
    <>
      <PageHeader title={t('iam.role.title')} description={t('iam.role.description')} />
      <RoleListPanel api={client.iam} />
    </>
  );
}
