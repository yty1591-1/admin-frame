import { isAppError, type AdminIamApi, type Permission } from '@admin-frame/api-client';
import type { Channel } from '@admin-frame/shared';
import {
  Button,
  Input,
  Select,
  Space,
  StatusTag,
  TableShell,
  Typography,
  type TableColumnsType,
} from '@admin-frame/ui';
import { useEffect, useState } from 'react';
import {
  permissionChannelMeta,
  permissionKindMeta,
  permissionStatusMeta,
} from '../../entities/permission';
import { useListParams } from '../../shared';
import { usePermissionCatalog } from './use-permission-catalog';

/** 必须是模块级常量：`useListParams` 以引用判断筛选键集合（§8.1）。 */
const FILTER_KEYS = ['keyword', 'channel'] as const;

const CHANNEL_OPTIONS = [
  { value: 'admin', label: 'A 端' },
  { value: 'biz', label: 'B 端' },
] as const;

const columns: TableColumnsType<Permission> = [
  {
    title: '权限码',
    dataIndex: 'code',
    key: 'code',
    width: 260,
    render: (code: string) => <Typography.Text code>{code}</Typography.Text>,
  },
  { title: '名称', dataIndex: 'name', key: 'name' },
  {
    title: '端别',
    dataIndex: 'channel',
    key: 'channel',
    width: 100,
    render: (channel: Permission['channel']) => permissionChannelMeta[channel].label,
  },
  {
    title: '类型',
    dataIndex: 'kind',
    key: 'kind',
    width: 100,
    render: (kind: Permission['kind']) => permissionKindMeta[kind].label,
  },
  {
    title: '状态',
    dataIndex: 'status',
    key: 'status',
    width: 100,
    render: (status: Permission['status']) => (
      <StatusTag
        tone={permissionStatusMeta[status].tone}
        label={permissionStatusMeta[status].label}
      />
    ),
  },
];

export function PermissionCatalogPanel({ api }: { api: AdminIamApi }) {
  const { page, pageSize, filters, setPage, setPageSize, setFilter, resetFilters } =
    useListParams(FILTER_KEYS);
  const query = usePermissionCatalog(api, {
    page,
    pageSize,
    keyword: filters.keyword,
    channel: filters.channel as Channel | undefined,
  });
  // 输入框允许中间态；提交（回车/查询/清空）后才写入 URL 触发请求。
  const [keyword, setKeyword] = useState(filters.keyword ?? '');

  useEffect(() => {
    setKeyword(filters.keyword ?? '');
  }, [filters.keyword]);

  return (
    <TableShell<Permission>
      columns={columns}
      rows={query.data?.items}
      rowKey="code"
      total={query.data?.total ?? 0}
      page={page}
      pageSize={pageSize}
      loading={query.isFetching}
      error={
        query.isError
          ? {
              message: '权限目录加载失败，请检查筛选条件或稍后重试。',
              traceId: isAppError(query.error) ? query.error.traceId : undefined,
            }
          : null
      }
      emptyText="暂无权限点"
      toolbar={
        <Space wrap>
          <Input
            aria-label="权限关键字"
            allowClear
            placeholder="搜索权限码或名称"
            style={{ width: 280 }}
            value={keyword}
            onChange={(event) => {
              const next = event.target.value;
              setKeyword(next);
              if (next === '') {
                setFilter('keyword', null);
              }
            }}
            onPressEnter={() => {
              setFilter('keyword', keyword);
            }}
          />
          <Button
            type="primary"
            onClick={() => {
              setFilter('keyword', keyword);
            }}
          >
            查询
          </Button>
          <Select<string>
            aria-label="端别"
            allowClear
            placeholder="全部端别"
            style={{ width: 160 }}
            options={[...CHANNEL_OPTIONS]}
            value={filters.channel ?? null}
            onChange={(value) => {
              setFilter('channel', value ?? null);
            }}
          />
          <Button
            onClick={() => {
              resetFilters();
            }}
          >
            重置
          </Button>
        </Space>
      }
      onPageChange={(nextPage, nextPageSize) => {
        if (nextPageSize !== pageSize) {
          setPageSize(nextPageSize);
          return;
        }
        setPage(nextPage);
      }}
      onRetry={() => {
        void query.refetch();
      }}
    />
  );
}
