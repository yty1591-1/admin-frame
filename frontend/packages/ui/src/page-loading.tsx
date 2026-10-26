import { useI18n } from '@admin-frame/i18n';
import { Spin } from 'antd';

export type PageLoadingProps = {
  tip?: string | undefined;
};

/** 路由级加载态；作为 Suspense fallback 使用。 */
export function PageLoading({ tip }: PageLoadingProps) {
  const { t } = useI18n();

  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 12,
        minHeight: 240,
      }}
    >
      <Spin size="large" />
      <span>{tip ?? t('common.loading')}</span>
    </div>
  );
}
