import { useI18n } from '@admin-frame/i18n';
import { ErrorState } from '@admin-frame/ui';

export default function ForbiddenPage() {
  const { t } = useI18n();

  return (
    <ErrorState
      title={t('permission.state.deniedTitle')}
      description={t('permission.state.deniedDescription')}
    />
  );
}
