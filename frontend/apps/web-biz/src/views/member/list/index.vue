<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { MemberApi } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import { Button, Space, Table, Tag } from 'ant-design-vue';

import { useVbenForm } from '#/adapter/form';
import { getMemberListApi } from '#/api';

defineOptions({ name: 'MemberList' });

/**
 * 查询条件：由 schema 驱动，无需手写表单模板。
 */
const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Input',
      componentProps: {
        allowClear: true,
        placeholder: '请输入成员名称',
      },
      fieldName: 'name',
      label: '成员名称',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: '启用', value: 1 },
          { label: '停用', value: 0 },
        ],
        placeholder: '请选择状态',
      },
      fieldName: 'status',
      label: '状态',
    },
  ],
  showDefaultActions: false,
});

const loading = ref(false);
const rows = ref<MemberApi.MemberItem[]>([]);

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'name', key: 'name', title: '成员名称' },
  { dataIndex: 'status', key: 'status', title: '状态', width: 100 },
  { dataIndex: 'remark', ellipsis: true, key: 'remark', title: '备注' },
  { dataIndex: 'createTime', key: 'createTime', title: '创建时间', width: 200 },
];

async function fetchList() {
  loading.value = true;
  try {
    const values = await queryFormApi.getValues();
    const { items, total } = await getMemberListApi({
      name: values.name || undefined,
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: values.status ?? undefined,
    });
    rows.value = items;
    pagination.total = total;
  } catch {
    // 后端尚未提供「商户成员列表」端点（见 src/api/system/member.ts）：
    // 保持空表并继续渲染，避免登录后首屏被错误提示打断。
    rows.value = [];
    pagination.total = 0;
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
    description="商户成员列表（示例模块，可作为其他业务模块的模板）"
    title="成员管理"
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
      row-key="id"
      size="middle"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'status'">
          <Tag :color="record.status === 1 ? 'success' : 'default'">
            {{ record.status === 1 ? '启用' : '停用' }}
          </Tag>
        </template>
      </template>
    </Table>
  </Page>
</template>
