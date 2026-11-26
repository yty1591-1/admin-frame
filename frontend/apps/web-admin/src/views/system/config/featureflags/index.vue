<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../../../iam/components/types';

import type { FeatureFlag } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import { Button, message, Table, Tag } from 'ant-design-vue';

import { createFeatureFlagApi, listFeatureFlagsApi } from '#/api';
import { listQueryCardClass, withListQueryForm } from '#/utils/list-query-form';

import FormModal from '../../../iam/components/form-modal.vue';

defineOptions({ name: 'SystemConfigFeatureFlags' });

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
        component: 'Select',
        componentProps: {
          allowClear: true,
          options: [
            { label: '启用', value: 'enabled' },
            { label: '关闭', value: 'disabled' },
            { label: '已到期', value: 'expired' },
          ],
          placeholder: '状态',
        },
        fieldName: 'state',
        label: '状态',
      },
    ],
  }),
);

const loading = ref(false);
const rows = ref<FeatureFlag[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'key', key: 'key', title: '特性键', width: 220 },
  { dataIndex: 'description', key: 'description', title: '描述', width: 260 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 110 },
  {
    dataIndex: 'default_enabled',
    key: 'default_enabled',
    title: '默认值',
    width: 100,
  },
  { dataIndex: 'version', key: 'version', title: '版本', width: 90 },
];

const statusMap: Record<string, { color: string; label: string }> = {
  disabled: { color: 'default', label: '关闭' },
  enabled: { color: 'success', label: '启用' },
  expired: { color: 'warning', label: '已到期' },
};

const createOpen = ref(false);
const createSubmitting = ref(false);
const createFields: FormField[] = [
  {
    label: '特性键',
    name: 'key',
    placeholder: '如 notification.retry',
    required: true,
    span: 2,
  },
  { label: '描述', name: 'description', required: true, span: 2 },
  {
    component: 'datetime',
    label: '到期时间',
    name: 'expires_at',
    placeholder: '非永久开关必填',
    span: 2,
  },
  {
    component: 'switch',
    label: '默认开启',
    name: 'default_enabled',
    span: 1,
  },
  {
    component: 'switch',
    label: '永久开关',
    name: 'permanent',
    span: 1,
  },
];

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listFeatureFlagsApi({
      page: pagination.current,
      pageSize: pagination.pageSize,
      state: values.state || undefined,
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

async function handleCreate(payload: Record<string, any>) {
  const permanent = Boolean(payload.permanent);
  const expiresAt =
    typeof payload.expires_at === 'string'
      ? payload.expires_at
      : payload.expires_at?.toISOString?.();
  if (!permanent && !expiresAt) {
    message.warning('非永久开关必须填写到期时间');
    return;
  }
  createSubmitting.value = true;
  try {
    await createFeatureFlagApi({
      default_enabled: Boolean(payload.default_enabled),
      description: payload.description,
      expires_at: permanent ? undefined : expiresAt,
      key: payload.key,
      permanent,
    });
    message.success('特性开关已创建');
    createOpen.value = false;
    await fetchList();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    createSubmitting.value = false;
  }
}

onMounted(fetchList);
</script>

<template>
  <Page>
    <div :class="listQueryCardClass">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
        <QueryForm class="min-w-0 flex-1" />
        <Button
          v-access:code="'featureflag:write'"
          type="primary"
          @click="createOpen = true"
        >
          新建开关
        </Button>
      </div>

      <Table
        :columns="columns"
        :data-source="rows"
        :loading="loading"
        :pagination="pagination"
        row-key="key"
        size="middle"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <Tag :color="statusMap[record.status]?.color ?? 'default'">
              {{ statusMap[record.status]?.label ?? record.status }}
            </Tag>
          </template>
          <template v-else-if="column.key === 'default_enabled'">
            {{ record.default_enabled ? '开启' : '关闭' }}
          </template>
        </template>
      </Table>
    </div>

    <FormModal
      v-model:open="createOpen"
      :fields="createFields"
      :loading="createSubmitting"
      submit-text="创建"
      title="新建特性开关"
      @submit="handleCreate"
    />
  </Page>
</template>
