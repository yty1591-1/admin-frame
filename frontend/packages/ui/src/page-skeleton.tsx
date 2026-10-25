import { Skeleton } from 'antd';

export type PageSkeletonProps = {
  /** 占位段落行数，默认 4。 */
  rows?: number | undefined;
};

/** 路由级首屏骨架；列表区域的首屏加载由 TableShell 的 loading 负责（§3.1）。 */
export function PageSkeleton({ rows = 4 }: PageSkeletonProps) {
  return (
    <div role="status" aria-label="页面加载中" aria-live="polite">
      <Skeleton active title paragraph={{ rows }} />
    </div>
  );
}
