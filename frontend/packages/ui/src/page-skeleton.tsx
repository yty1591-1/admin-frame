import { useI18n } from '@admin-frame/i18n';
import { Skeleton } from 'antd';

export type PageSkeletonProps = {
  /** 占位段落行数，默认 4。 */
  rows?: number | undefined;
};

/** 路由级首屏骨架；列表区域的首屏加载由 TableShell 的 loading 负责（§3.1）。 */
export function PageSkeleton({ rows = 4 }: PageSkeletonProps) {
  const { t } = useI18n();

  return (
    <div role="status" aria-label={t('common.pageLoading')} aria-live="polite">
      <Skeleton active title paragraph={{ rows }} />
    </div>
  );
}
