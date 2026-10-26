import { useI18n } from '@admin-frame/i18n';
import { Card, Space, Typography } from '@admin-frame/ui';

export default function HomePage() {
  const { t } = useI18n();

  return (
    <Space direction="vertical" size="large" style={{ width: '100%' }}>
      <Card title={t('common.homeCardTitle')}>
        <Typography.Paragraph>{t('common.homeCardDescription')}</Typography.Paragraph>
        <Typography.Paragraph type="secondary">
          {t('common.contractVersion', { commit: __CONTRACT_COMMIT__ })}
        </Typography.Paragraph>
      </Card>
    </Space>
  );
}
