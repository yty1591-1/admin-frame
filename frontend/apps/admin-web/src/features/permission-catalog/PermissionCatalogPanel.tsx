import type { AdminIamApi, Permission } from '@admin-frame/api-client';
import { describeError, useI18n } from '@admin-frame/i18n';
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

const CHANNEL_VALUES = ['admin', 'biz'] as const;

export function PermissionCatalogPanel({ api }: { api: AdminIamApi }) {
  const { t } = useI18n();
  const columns: TableColumnsType<Permission> = [
    {
      title: t('permission.catalog.col.code'),
      dataIndex: 'code',
      key: 'code',
      width: 260,
      render: (code: string) => <Typography.Text code>{code}</Typography.Text>,
    },
    { title: t('permission.catalog.col.name'), dataIndex: 'name', key: 'name' },
    {
      title: t('permission.catalog.col.channel'),
      dataIndex: 'channel',
      key: 'channel',
      width: 100,
      render: (channel: Permission['channel']) => t(permissionChannelMeta[channel].labelKey),
    },
    {
      title: t('permission.catalog.col.kind'),
      dataIndex: 'kind',
      key: 'kind',
      width: 100,
      render: (kind: Permission['kind']) => t(permissionKindMeta[kind].labelKey),
    },
    {
      title: t('permission.catalog.col.status'),
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (status: Permission['status']) => (
        <StatusTag
          tone={permissionStatusMeta[status].tone}
          label={t(permissionStatusMeta[status].labelKey)}
        />
      ),
    },
  ];
  const { page, pageSize, filters, setPage, setPageSize, setFilter, resetFilters } =
    useListParams(FILTER_KEYS);
  const query = usePermissionCatalog(api, {
    page,
    pageSize,
    keyword: filters.keyword,
    channel: filters.channel as Channel | undefined,
  });
  const listError = query.isError ? describeError(query.error) : null;
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
        listError === null
          ? null
          : {
              message: t(listError.messageKey),
              traceId: listError.traceId,
            }
      }
      emptyText={t('permission.catalog.empty')}
      toolbar={
        <Space wrap>
          <Input
            aria-label={t('permission.catalog.keywordAria')}
            allowClear
            placeholder={t('permission.catalog.searchPlaceholder')}
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
            {t('common.search')}
          </Button>
          <Select<string>
            aria-label={t('permission.catalog.channelAria')}
            allowClear
            placeholder={t('permission.catalog.channelAll')}
            style={{ width: 160 }}
            options={CHANNEL_VALUES.map((value) => ({
              value,
              label: t(permissionChannelMeta[value].labelKey),
            }))}
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
            {t('common.reset')}
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
