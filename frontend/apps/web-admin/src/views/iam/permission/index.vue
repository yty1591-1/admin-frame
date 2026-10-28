<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';
import type { Permission } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import { Button, Space, Table, Tag } from 'ant-design-vue';

import { useVbenForm } from '#/adapter/form';
import { listPermissionsApi } from '#/api';

defineOptions({ name: 'IamPermission' });

const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Input',
      componentProps: {
        allowClear: true,
        placeholder: '权限码 / 名称',
      },
      fieldName: 'keyword',
      label: '关键字',
    },
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
      label: '端别',
    },
  ],
  showDefaultActions: false,
});

const loading = ref(false);
const rows = ref<Permission[]>([]);

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'code', key: 'code', title: '权限码', width: 220 },
  { dataIndex: 'name', key: 'name', title: '名称', width: 180 },
  { dataIndex: 'channel', key: 'channel', title: '端别', width: 90 },
  { dataIndex: 'resource', key: 'resource', title: '资源', width: 100 },
  {
    dataIndex: 'data_bearing',
    key: 'data_bearing',
    title: '数据敏感',
    width: 100,
  },
  { dataIndex: 'status', key: 'status', title: '状态', width: 100 },
  { dataIndex: 'sort', key: 'sort', title: '排序', width: 80 },
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

function handleSearch() {
  pagination.current = 1;
  fetchList();
}

async function handleReset() {
  await queryFormApi.reset();
  handleSearch();
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

onMounted(fetchList);
</script>

<template>
  <Page
    description="权限目录由迁移维护、页面只读；授权入口在角色与绑定页"
    title="权限目录"
  >
    <div class="bg-card mb-4 rounded-lg p-4">
      <QueryForm />
      <Space class="mt-2">
        <Button type="primary" @click="handleSearch">查询</Button>
        <Button @click="handleReset">重置</Button>
      </Space>
    </div>

    <Table
      :columns="columns"
      :data-source="rows"
      :loading="loading"
      :pagination="pagination"
      row-key="code"
      size="middle"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'data_bearing'">
          <Tag :color="record.data_bearing ? 'orange' : 'default'">
            {{ record.data_bearing ? '是' : '否' }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'status'">
          <Tag :color="record.status === 'active' ? 'success' : 'default'">
            {{ record.status === 'active' ? '生效' : '已弃用' }}
          </Tag>
        </template>
      </template>
    </Table>
  </Page>
</template>
