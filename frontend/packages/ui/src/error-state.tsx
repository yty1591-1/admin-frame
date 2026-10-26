import { useI18n } from '@admin-frame/i18n';
import { Button, Space, Typography } from 'antd';

export type ErrorStateProps = {
  title: string;
  description?: string | undefined;
  /** 展示可复制的 trace_id，禁止透传原始 message（§9.3）。 */
  traceId?: string | undefined;
  onRetry?: (() => void) | undefined;
};

export function ErrorState({ title, description, traceId, onRetry }: ErrorStateProps) {
  const { t } = useI18n();

  return (
    <div role="alert" style={{ padding: '48px 0', textAlign: 'center' }}>
      <Space direction="vertical" size={8} align="center">
        <Typography.Title level={4} style={{ margin: 0 }}>
          {title}
        </Typography.Title>
        {description ? <Typography.Text type="secondary">{description}</Typography.Text> : null}
        {traceId ? (
          <Typography.Text type="secondary" copyable={{ text: traceId }}>
            {t('common.traceId', { traceId })}
          </Typography.Text>
        ) : null}
        {onRetry ? (
          <Button type="primary" onClick={onRetry}>
            {t('common.retry')}
          </Button>
        ) : null}
      </Space>
    </div>
  );
}
