import { useI18n } from '@admin-frame/i18n';
import { ErrorState } from '@admin-frame/ui';

export default function NotFoundPage() {
  const { t } = useI18n();

  return <ErrorState title={t('nav.notFound')} description={t('common.notFoundDescriptionBiz')} />;
}
