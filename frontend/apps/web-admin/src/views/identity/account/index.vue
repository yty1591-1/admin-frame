<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { AccountSummary } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import { Button, Descriptions, Drawer, Tag } from 'ant-design-vue';

import { getAccountApi, listAccountsApi } from '#/api';
import { AdminListTable } from '#/components/admin-list-table';
import { listQueryCardClass, withListQueryForm } from '#/utils/list-query-form';

defineOptions({ name: 'IdentityAccount' });

const [QueryForm, queryFormApi] = useVbenForm(
  withListQueryForm({
    handleReset: async () => {
      await queryFormApi.reset();
      handleSearch();
    },
    handleSubmit: async () => {
      handleSearch();
    },
    schema: [
      {
        component: 'Input',
        componentProps: { allowClear: true, placeholder: '输入关键词' },
        fieldName: 'keyword',
        label: '关键词',
      },
      {
        component: 'Select',
        componentProps: {
          allowClear: true,
          options: [
            { label: '待激活', value: 'pending' },
            { label: '正常', value: 'active' },
            { label: '锁定', value: 'locked' },
            { label: '停用', value: 'disabled' },
          ],
          placeholder: '状态',
        },
        fieldName: 'status',
        label: '状态',
      },
    ],
  }),
);

const loading = ref(false);
const rows = ref<AccountSummary[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'subject', key: 'subject', title: '登录主体', width: 200 },
  { dataIndex: 'id', key: 'id', title: '账号 ID', width: 220 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 110 },
  { dataIndex: 'created_at', key: 'created_at', title: '创建于', width: 180 },
  { key: 'actions', title: '操作', width: 100 },
];

const statusMap: Record<string, { color: string; label: string }> = {
  active: { color: 'success', label: '正常' },
  disabled: { color: 'default', label: '停用' },
  locked: { color: 'warning', label: '锁定' },
  pending: { color: 'processing', label: '待激活' },
};

const detailOpen = ref(false);
const detailLoading = ref(false);
const detail = ref<AccountSummary>();

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listAccountsApi({
      keyword: values.keyword || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: values.status || undefined,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } finally {
    loading.value = false;
  }
}

function handleSearch() {
  pagination.current = 1;
  fetchList();
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

async function openDetail(record: AccountSummary) {
  detailOpen.value = true;
  detailLoading.value = true;
  try {
    detail.value = await getAccountApi(record.id);
  } catch {
    detail.value = record;
  } finally {
    detailLoading.value = false;
  }
}

onMounted(fetchList);
</script>

<template>
  <Page>
    <div :class="listQueryCardClass">
      <QueryForm class="mb-3" />

      <AdminListTable
        :columns="columns"
        :data-source="rows"
        :loading="loading"
        :pagination="pagination"
        row-key="id"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <Tag :color="statusMap[record.status]?.color ?? 'default'">
              {{ statusMap[record.status]?.label ?? record.status }}
            </Tag>
          </template>
          <template v-else-if="column.key === 'actions'">
            <Button
              size="small"
              type="link"
              @click="openDetail(record as AccountSummary)"
            >
              详情
            </Button>
          </template>
        </template>
      </AdminListTable>
    </div>

    <Drawer v-model:open="detailOpen" title="管理员账号详情" width="560">
      <Descriptions
        v-if="detail"
        :column="1"
        :loading="detailLoading"
        bordered
        size="small"
      >
        <Descriptions.Item label="登录主体">
          {{ detail.subject }}
        </Descriptions.Item>
        <Descriptions.Item label="账号 ID">{{ detail.id }}</Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag :color="statusMap[detail.status]?.color ?? 'default'">
            {{ statusMap[detail.status]?.label ?? detail.status }}
          </Tag>
        </Descriptions.Item>
        <Descriptions.Item label="端别">
          {{ detail.channel }}
        </Descriptions.Item>
        <Descriptions.Item label="令牌版本">
          {{ detail.token_version }}
        </Descriptions.Item>
        <Descriptions.Item label="锁定至">
          {{ detail.locked_until || '—' }}
        </Descriptions.Item>
        <Descriptions.Item label="创建于">
          {{ detail.created_at }}
        </Descriptions.Item>
      </Descriptions>
      <p class="text-muted-foreground mt-4 text-xs">
        锁定、解锁、启停、重置密码 / MFA 与会话撤销按账号状态在详情中开放。
      </p>
    </Drawer>
  </Page>
</template>
