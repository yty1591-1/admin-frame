<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';
import type { Dayjs } from 'dayjs';

import type { AuditEvent } from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';

import { Page } from '@vben/common-ui';

import {
  Button,
  DatePicker,
  Descriptions,
  Drawer,
  Input,
  message,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';
import dayjs from 'dayjs';

import {
  createAuditExportJobApi,
  exportAuditEventsCsvApi,
  listAuditEventsApi,
} from '#/api';

defineOptions({ name: 'SystemAudit' });

const router = useRouter();
const { RangePicker } = DatePicker;

const loading = ref(false);
const exporting = ref(false);
const rows = ref<AuditEvent[]>([]);
const range = ref<[Dayjs, Dayjs]>([
  dayjs().subtract(23, 'hour').startOf('hour'),
  dayjs(),
]);
const filters = reactive({
  actor_id: '',
  action: '',
  trace_id: '',
});
const detailOpen = ref(false);
const detail = ref<AuditEvent>();

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const columns: TableColumnsType = [
  { dataIndex: 'occurred_at', key: 'occurred_at', title: '时间', width: 190 },
  { dataIndex: 'actor_id', key: 'actor_id', title: '操作者', width: 160 },
  { dataIndex: 'action', key: 'action', title: '动作', width: 200 },
  {
    dataIndex: 'resource_type',
    key: 'resource_type',
    title: '资源类型',
    width: 140,
  },
  {
    dataIndex: 'resource_id',
    key: 'resource_id',
    title: '资源 ID',
    width: 200,
  },
  { dataIndex: 'outcome', key: 'outcome', title: '结果', width: 90 },
  { dataIndex: 'trace_id', key: 'trace_id', title: 'Trace', width: 220 },
  { key: 'actions', title: '操作', width: 90 },
];

const outcomeColor = computed(() => (outcome: string) => {
  if (outcome === 'success') return 'success';
  if (outcome === 'denied') return 'warning';
  return 'error';
});

function formatTime(value: string): string {
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm:ss') : value;
}

async function fetchList() {
  const [start, end] = range.value;
  if (!start || !end) {
    message.warning('请选择时间范围');
    return;
  }
  loading.value = true;
  try {
    const result = await listAuditEventsApi({
      from: start.toISOString(),
      to: end.toISOString(),
      page: pagination.current,
      pageSize: pagination.pageSize,
      ...(filters.actor_id ? { actor_id: filters.actor_id.trim() } : {}),
      ...(filters.action ? { action: filters.action.trim() } : {}),
      ...(filters.trace_id ? { trace_id: filters.trace_id.trim() } : {}),
    });
    rows.value = result.items;
    pagination.total = result.total;
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    loading.value = false;
  }
}

function search() {
  pagination.current = 1;
  fetchList();
}

function reset() {
  filters.actor_id = '';
  filters.action = '';
  filters.trace_id = '';
  range.value = [dayjs().subtract(23, 'hour').startOf('hour'), dayjs()];
  pagination.current = 1;
  fetchList();
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

function openDetail(record: AuditEvent) {
  detail.value = record;
  detailOpen.value = true;
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

function isAsyncExportUnavailable(error: unknown): boolean {
  const status = (error as { response?: { status?: number } })?.response
    ?.status;
  return status === 503 || status === 409;
}

async function handleExport() {
  const [start, end] = range.value;
  if (!start || !end) {
    message.warning('请选择时间范围');
    return;
  }
  const params = {
    from: start.toISOString(),
    to: end.toISOString(),
    ...(filters.actor_id ? { actor_id: filters.actor_id.trim() } : {}),
    ...(filters.action ? { action: filters.action.trim() } : {}),
    ...(filters.trace_id ? { trace_id: filters.trace_id.trim() } : {}),
  };
  exporting.value = true;
  try {
    const job = await createAuditExportJobApi(params);
    message.success(`已创建导出任务 ${job.export_id}`);
    await router.push({
      path: '/system/audit/exports',
      query: { export_id: job.export_id },
    });
  } catch (error) {
    if (!isAsyncExportUnavailable(error)) {
      return;
    }
    try {
      const blob = await exportAuditEventsCsvApi(params);
      downloadBlob(blob, `audit-events-${Date.now()}.csv`);
      message.success('异步导出未启用，已改用同步导出');
    } catch {}
  } finally {
    exporting.value = false;
  }
}

onMounted(fetchList);
</script>

<template>
  <Page
    description="以时间、主体、动作和链路定位平台审计事件。"
    title="审计事件"
  >
    <template #extra>
      <Button
        v-access:code="'audit:read'"
        :loading="exporting"
        type="primary"
        @click="handleExport"
      >
        导出审计
      </Button>
    </template>

    <div class="bg-card mb-4 rounded-lg p-4">
      <Space wrap>
        <RangePicker
          v-model:value="range"
          :allow-clear="false"
          format="YYYY-MM-DD HH:mm:ss"
          show-time
        />
        <Input
          v-model:value="filters.actor_id"
          allow-clear
          placeholder="操作者 ID"
          style="width: 180px"
        />
        <Input
          v-model:value="filters.action"
          allow-clear
          placeholder="动作，如 iam.role.create"
          style="width: 220px"
        />
        <Input
          v-model:value="filters.trace_id"
          allow-clear
          placeholder="Trace ID"
          style="width: 240px"
        />
        <Button type="primary" @click="search">查询</Button>
        <Button @click="reset">重置</Button>
      </Space>
    </div>

    <Table
      :columns="columns"
      :data-source="rows"
      :loading="loading"
      :pagination="pagination"
      :scroll="{ x: 1500 }"
      row-key="event_id"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'occurred_at'">
          {{ formatTime((record as AuditEvent).occurred_at) }}
        </template>
        <template v-else-if="column.key === 'outcome'">
          <Tag :color="outcomeColor((record as AuditEvent).outcome)">
            {{ (record as AuditEvent).outcome }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'trace_id'">
          <span class="text-xs">{{
            (record as AuditEvent).trace_id || '—'
          }}</span>
        </template>
        <template v-else-if="column.key === 'actions'">
          <Button
            size="small"
            type="link"
            @click="openDetail(record as AuditEvent)"
          >
            详情
          </Button>
        </template>
      </template>
    </Table>

    <Drawer v-model:open="detailOpen" title="审计事件详情" width="560">
      <Descriptions v-if="detail" :column="1" bordered size="small">
        <Descriptions.Item label="事件 ID">
          {{ detail.event_id }}
        </Descriptions.Item>
        <Descriptions.Item label="时间">
          {{ formatTime(detail.occurred_at) }}
        </Descriptions.Item>
        <Descriptions.Item label="操作者">
          {{ detail.actor_id }}（{{ detail.actor_type || 'user' }}）
        </Descriptions.Item>
        <Descriptions.Item label="端别">{{ detail.channel }}</Descriptions.Item>
        <Descriptions.Item label="动作">{{ detail.action }}</Descriptions.Item>
        <Descriptions.Item label="资源">
          {{ detail.resource_type }} / {{ detail.resource_id }}
        </Descriptions.Item>
        <Descriptions.Item label="结果">{{ detail.outcome }}</Descriptions.Item>
        <Descriptions.Item label="服务">{{ detail.service }}</Descriptions.Item>
        <Descriptions.Item label="Trace ID">
          {{ detail.trace_id }}
        </Descriptions.Item>
        <Descriptions.Item v-if="detail.reason" label="原因">
          {{ detail.reason }}
        </Descriptions.Item>
        <Descriptions.Item v-if="detail.attrs" label="附加">
          <pre class="whitespace-pre-wrap break-all">{{
            JSON.stringify(detail.attrs, null, 2)
          }}</pre>
        </Descriptions.Item>
      </Descriptions>
    </Drawer>
  </Page>
</template>
