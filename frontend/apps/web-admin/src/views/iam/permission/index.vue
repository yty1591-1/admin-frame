<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { Permission } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { useVbenForm } from '@vben/app-kernel/adapter/form';
import { Page } from '@vben/common-ui';

import { Button, Descriptions, Modal, Tag } from 'ant-design-vue';

import { listPermissionsApi } from '#/api';
import { AdminListTable } from '#/components/admin-list-table';
import { listQueryCardClass, withListQueryForm } from '#/utils/list-query-form';

defineOptions({ name: 'IamPermission' });

const [QueryForm, queryFormApi] = useVbenForm(
  withListQueryForm({
    handleReset: async () => {
      await queryFormApi.reset();
      pagination.current = 1;
      await fetchList();
    },
    handleSubmit: async () => {
      pagination.current = 1;
      await fetchList();
    },
    schema: [
      {
        component: 'Select',
        componentProps: {
          allowClear: true,
          options: [
            { label: 'A 端', value: 'admin' },
            { label: 'B 端', value: 'biz' },
          ],
          placeholder: '端别',
        },
        fieldName: 'channel',
        formItemClass: 'w-[156px] max-w-full',
        label: '端别',
      },
      {
        component: 'Input',
        componentProps: {
          allowClear: true,
          placeholder: '输入关键词',
        },
        fieldName: 'keyword',
        formItemClass: 'w-[220px] max-w-full',
        label: '关键字',
      },
    ],
  }),
);

const loading = ref(false);
const rows = ref<Permission[]>([]);
const detailOpen = ref(false);
const detail = ref<Permission>();

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'name', key: 'name', title: '权限名称', width: 180 },
  { dataIndex: 'code', key: 'code', title: '权限编码', width: 220 },
  { dataIndex: 'resource', key: 'resource', title: '资源', width: 120 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 100 },
  { key: 'actions', title: '操作', width: 100 },
];

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const result = await listPermissionsApi({
      channel: values.channel || undefined,
      keyword: values.keyword || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } finally {
    loading.value = false;
  }
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

function openDetail(record: Permission) {
  detail.value = record;
  detailOpen.value = true;
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
        :min-scroll-x="760"
        :pagination="pagination"
        row-key="code"
        @change="handleTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <Tag :color="record.status === 'active' ? 'success' : 'default'">
              {{ record.status === 'active' ? '启用' : '已弃用' }}
            </Tag>
          </template>
          <template v-else-if="column.key === 'actions'">
            <Button type="link" @click="openDetail(record)">详情</Button>
          </template>
        </template>
      </AdminListTable>
    </div>

    <Modal
      v-model:open="detailOpen"
      :footer="null"
      destroy-on-close
      title="权限详情"
      width="560px"
    >
      <Descriptions v-if="detail" :column="1" bordered size="small">
        <Descriptions.Item label="权限名称">
          {{ detail.name }}
        </Descriptions.Item>
        <Descriptions.Item label="权限编码">
          {{ detail.code }}
        </Descriptions.Item>
        <Descriptions.Item label="资源">
          {{ detail.resource }}
        </Descriptions.Item>
        <Descriptions.Item label="端别">
          {{ detail.channel }}
        </Descriptions.Item>
        <Descriptions.Item label="数据敏感">
          {{ detail.data_bearing ? '是' : '否' }}
        </Descriptions.Item>
        <Descriptions.Item label="状态">
          {{ detail.status === 'active' ? '启用' : '已弃用' }}
        </Descriptions.Item>
        <Descriptions.Item label="排序">
          {{ detail.sort }}
        </Descriptions.Item>
        <Descriptions.Item v-if="detail.description" label="说明">
          {{ detail.description }}
        </Descriptions.Item>
      </Descriptions>
    </Modal>
  </Page>
</template>
