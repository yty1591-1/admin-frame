import { useI18n } from '@admin-frame/i18n';
import { Empty } from 'antd';

export type EmptyStateProps = {
  description?: string | undefined;
};

/** 统一空态；“无数据”与“无权限/不可访问”由调用方文案区分（§11.2）。 */
export function EmptyState({ description }: EmptyStateProps) {
  const { t } = useI18n();

  return (
    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={description ?? t('common.empty')} />
  );
}
