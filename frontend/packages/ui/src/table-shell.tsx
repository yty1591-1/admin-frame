import { useI18n } from '@admin-frame/i18n';
import { Card, Table, type TableColumnsType } from 'antd';
import type { ReactNode } from 'react';
import { EmptyState } from './empty-state';
import { ErrorState } from './error-state';
import { PageSkeleton } from './page-skeleton';

export type TableShellError = {
  message: string;
  traceId?: string | undefined;
};

export type TableShellProps<T> = {
  columns: TableColumnsType<T>;
  /** undefined 表示首屏尚未加载；空数组表示无数据。 */
  rows: readonly T[] | undefined;
  rowKey: keyof T | ((row: T) => string);
  total: number;
  page: number;
  pageSize: number;
  loading: boolean;
  /** 错误只传入文案与 trace_id，ui 不感知 AppError（§4）。 */
  error?: TableShellError | null | undefined;
  emptyText?: string | undefined;
  /** 表格上方的筛选区/批量操作，与表格共用同一容器。 */
  toolbar?: ReactNode | undefined;
  onPageChange: (page: number, pageSize: number) => void;
  onRetry?: (() => void) | undefined;
};

const PAGE_SIZE_OPTIONS = ['20', '50', '100'];

/**
 * 受控表格容器（§3.3）：分页、筛选与数据由调用方提供，ui 不感知 Query 与 URL。
 * 首屏骨架、错误重试、空态与服务端分页在此统一实现，避免各页重复。
 */
export function TableShell<T>({
  columns,
  rows,
  rowKey,
  total,
  page,
  pageSize,
  loading,
  error,
  emptyText,
  toolbar,
  onPageChange,
  onRetry,
}: TableShellProps<T>) {
  const { t } = useI18n();
  const header = toolbar === undefined ? null : <div style={{ marginBottom: 16 }}>{toolbar}</div>;

  if (error) {
    return (
      <Card>
        {header}
        <ErrorState
          title={t('common.listLoadFailedTitle')}
          description={error.message}
          traceId={error.traceId}
          onRetry={onRetry}
        />
      </Card>
    );
  }

  const firstLoad = loading && rows === undefined;

  return (
    <Card>
      {header}
      {firstLoad ? (
        <PageSkeleton rows={6} />
      ) : (
        <Table<T>
          rowKey={rowKey}
          size="middle"
          columns={columns}
          dataSource={rows ?? []}
          loading={loading}
          locale={{ emptyText: <EmptyState description={emptyText ?? t('common.empty')} /> }}
          pagination={{
            current: page,
            pageSize,
            total,
            showSizeChanger: true,
            pageSizeOptions: PAGE_SIZE_OPTIONS,
            showTotal: (count) => t('common.listTotal', { count }),
            onChange: (nextPage, nextPageSize) => {
              onPageChange(nextPage, nextPageSize);
            },
          }}
        />
      )}
    </Card>
  );
}
