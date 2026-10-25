import { Empty } from 'antd';

export type EmptyStateProps = {
  description?: string | undefined;
};

/** 统一空态；“无数据”与“无权限/不可访问”由调用方文案区分（§11.2）。 */
export function EmptyState({ description = '暂无数据' }: EmptyStateProps) {
  return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={description} />;
}
