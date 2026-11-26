<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { FormField } from '../components/types';

import type { Certification, CertificationStatus } from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  Descriptions,
  Drawer,
  message,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  createCertificationApi,
  getCertificationApi,
  listCertificationsApi,
} from '#/api';

import FormModal from '../components/form-modal.vue';

defineOptions({ name: 'IamCertification' });

const statusTabs: { key: '' | CertificationStatus; label: string }[] = [
  { key: '', label: '全部' },
  { key: 'active', label: '进行中' },
  { key: 'draft', label: '草稿' },
  { key: 'closed', label: '已关闭' },
];

const activeStatus = ref<'' | CertificationStatus>('');
const loading = ref(false);
const rows = ref<Certification[]>([]);
const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'name', key: 'name', title: '活动名称', width: 240 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 120 },
  { dataIndex: 'due_at', key: 'due_at', title: '到期时间', width: 180 },
  {
    dataIndex: 'task_count',
    key: 'task_count',
    title: '任务数',
    width: 100,
  },
  { key: 'actions', title: '操作', width: 100 },
];

const statusMap: Record<string, { color: string; label: string }> = {
  active: { color: 'processing', label: '进行中' },
  closed: { color: 'default', label: '已关闭' },
  draft: { color: 'warning', label: '草稿' },
};

const createOpen = ref(false);
const createSubmitting = ref(false);
const createFields: FormField[] = [
  { label: '活动名称', name: 'name', required: true, span: 2 },
  {
    component: 'datetime',
    label: '到期时间',
    name: 'due_at',
    required: true,
    span: 2,
  },
  {
    component: 'textarea',
    label: '原因',
    name: 'reason',
    rows: 2,
    span: 2,
  },
];

const detailOpen = ref(false);
const detailLoading = ref(false);
const detail = ref<Certification>();

const statusFilter = computed(() => activeStatus.value || undefined);

async function fetchList() {
  loading.value = true;
  try {
    const result = await listCertificationsApi({
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: statusFilter.value,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } finally {
    loading.value = false;
  }
}

function selectStatus(key: '' | CertificationStatus) {
  activeStatus.value = key;
  pagination.current = 1;
  fetchList();
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

async function handleCreate(payload: Record<string, any>) {
  createSubmitting.value = true;
  try {
    const dueAt =
      typeof payload.due_at === 'string'
        ? payload.due_at
        : payload.due_at?.toISOString?.();
    await createCertificationApi({
      due_at: dueAt,
      name: payload.name,
      reason: payload.reason || undefined,
    });
    message.success('复核活动已创建');
    createOpen.value = false;
    await fetchList();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    createSubmitting.value = false;
  }
}

async function openDetail(record: Certification) {
  detailOpen.value = true;
  detailLoading.value = true;
  try {
    detail.value = await getCertificationApi(record.id);
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
    <div class="bg-card rounded-lg p-4">
      <div class="mb-4 flex flex-wrap gap-4 border-b pb-3">
        <button
          v-for="tab in statusTabs"
          :key="tab.key || 'all'"
          class="relative pb-2 text-sm transition-colors"
          :class="
            activeStatus === tab.key
              ? 'text-primary font-semibold'
              : 'text-muted-foreground'
          "
          type="button"
          @click="selectStatus(tab.key)"
        >
          {{ tab.label }}
          <span
            v-if="activeStatus === tab.key"
            class="bg-primary absolute bottom-0 left-0 h-0.5 w-full"
          ></span>
        </button>
      </div>

      <Space class="mb-3">
        <Button @click="fetchList">刷新</Button>
        <Button
          v-access:code="'iam:certification:write'"
          type="primary"
          @click="createOpen = true"
        >
          创建复核
        </Button>
      </Space>

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
            <Tag :color="statusMap[record.status]?.color ?? 'default'">
              {{ statusMap[record.status]?.label ?? record.status }}
            </Tag>
          </template>
          <template v-else-if="column.key === 'actions'">
            <Button
              size="small"
              type="link"
              @click="openDetail(record as Certification)"
            >
              详情
            </Button>
          </template>
        </template>
      </Table>
    </div>

    <FormModal
      v-model:open="createOpen"
      :fields="createFields"
      :loading="createSubmitting"
      submit-text="创建"
      title="创建复核活动"
      @submit="handleCreate"
    />

    <Drawer v-model:open="detailOpen" title="复核活动详情" width="560">
      <Descriptions
        v-if="detail"
        :column="1"
        :loading="detailLoading"
        bordered
        size="small"
      >
        <Descriptions.Item label="活动名称">
          {{ detail.name }}
        </Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag :color="statusMap[detail.status]?.color ?? 'default'">
            {{ statusMap[detail.status]?.label ?? detail.status }}
          </Tag>
        </Descriptions.Item>
        <Descriptions.Item label="到期时间">
          {{ detail.due_at }}
        </Descriptions.Item>
        <Descriptions.Item label="任务数">
          {{ detail.task_count ?? 0 }}
        </Descriptions.Item>
      </Descriptions>
      <p class="text-muted-foreground mt-4 text-xs">
        草稿经激活生成待复核任务；详情下设任务裁决与证据导出。
      </p>
    </Drawer>
  </Page>
</template>
