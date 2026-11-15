<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { ConfigItem, ConfigItemVersion } from '#/api';

import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';

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
  getConfigErrorMessage,
  getConfigItemApi,
  getConfigItemVersionApi,
  listConfigItemVersionsApi,
} from '#/api';

defineOptions({ name: 'SystemConfigVersions' });

const route = useRoute();
const router = useRouter();

const loading = ref(false);
const item = ref<ConfigItem>();
const rows = ref<ConfigItemVersion[]>([]);
const detailOpen = ref(false);
const detailLoading = ref(false);
const detail = ref<ConfigItemVersion>();
const compareOpen = ref(false);
const leftText = ref('');
const rightText = ref('');

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const itemId = computed(() => Number(route.query.id || 0));

const columns: TableColumnsType = [
  { dataIndex: 'version', key: 'version', title: '版本', width: 90 },
  { dataIndex: 'value_hash', key: 'value_hash', title: '哈希', width: 280 },
  {
    dataIndex: 'change_summary',
    key: 'change_summary',
    title: '摘要',
    width: 200,
  },
  { dataIndex: 'created_by', key: 'created_by', title: '作者', width: 160 },
  { dataIndex: 'created_at', key: 'created_at', title: '时间', width: 190 },
  { key: 'actions', title: '操作', width: 160 },
];

async function loadItem() {
  if (!itemId.value) {
    message.error('缺少配置项 id');
    return;
  }
  item.value = await getConfigItemApi(itemId.value);
}

async function fetchList() {
  if (!itemId.value) {
    return;
  }
  loading.value = true;
  try {
    await loadItem();
    const result = await listConfigItemVersionsApi(itemId.value, {
      page: pagination.current,
      pageSize: pagination.pageSize,
    });
    rows.value = result.items;
    pagination.total = result.total;
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载版本失败'));
  } finally {
    loading.value = false;
  }
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

async function openDetail(record: ConfigItemVersion) {
  detailOpen.value = true;
  detailLoading.value = true;
  try {
    detail.value = await getConfigItemVersionApi(itemId.value, record.version);
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载版本详情失败'));
    detailOpen.value = false;
  } finally {
    detailLoading.value = false;
  }
}

async function openCompare(record: ConfigItemVersion) {
  if (!item.value || item.value.current_version <= 0) {
    message.warning('尚无当前版本可对比');
    return;
  }
  try {
    const [selected, current] = await Promise.all([
      getConfigItemVersionApi(itemId.value, record.version),
      getConfigItemVersionApi(itemId.value, item.value.current_version),
    ]);
    leftText.value = selected.value ?? `(敏感项仅哈希)\n${selected.value_hash}`;
    rightText.value = current.value ?? `(敏感项仅哈希)\n${current.value_hash}`;
    compareOpen.value = true;
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载对比失败'));
  }
}

function goEdit() {
  router.push({
    path: '/system/config/edit',
    query: { id: String(itemId.value) },
  });
}

function goBack() {
  router.push('/system/config/items');
}

watch(
  () => route.query.id,
  () => {
    pagination.current = 1;
    fetchList();
  },
);

onMounted(fetchList);
</script>

<template>
  <Page
    description="版本只读；文本 diff 为当前版本对照所选版本（结构化 diff 属 P1）"
    title="版本历史"
  >
    <div v-if="item" class="bg-card mb-4 rounded-lg p-4">
      <Descriptions :column="3" size="small">
        <Descriptions.Item label="配置键">{{ item.key }}</Descriptions.Item>
        <Descriptions.Item label="环境 / 命名空间">
          {{ item.env }} / {{ item.namespace }}
        </Descriptions.Item>
        <Descriptions.Item label="当前版本">
          <Tag color="success">{{ item.current_version }}</Tag>
        </Descriptions.Item>
      </Descriptions>
      <Space class="mt-2">
        <Button
          v-access:code="'config:write'"
          :disabled="item.sensitive"
          type="primary"
          @click="goEdit"
        >
          编辑
        </Button>
        <Button @click="goBack">返回列表</Button>
      </Space>
    </div>

    <Table
      :columns="columns"
      :data-source="rows"
      :loading="loading"
      :pagination="pagination"
      :scroll="{ x: 1100 }"
      row-key="id"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'actions'">
          <Space>
            <Button
              size="small"
              type="link"
              @click="openDetail(record as ConfigItemVersion)"
            >
              详情
            </Button>
            <Button
              size="small"
              type="link"
              @click="openCompare(record as ConfigItemVersion)"
            >
              对比当前
            </Button>
          </Space>
        </template>
      </template>
    </Table>

    <Drawer
      v-model:open="detailOpen"
      :loading="detailLoading"
      title="版本详情"
      width="520"
    >
      <Descriptions v-if="detail" :column="1" bordered size="small">
        <Descriptions.Item label="版本">{{ detail.version }}</Descriptions.Item>
        <Descriptions.Item label="哈希">{{ detail.value_hash }}</Descriptions.Item>
        <Descriptions.Item label="摘要">
          {{ detail.change_summary || '—' }}
        </Descriptions.Item>
        <Descriptions.Item label="作者">{{ detail.created_by }}</Descriptions.Item>
        <Descriptions.Item label="时间">{{ detail.created_at }}</Descriptions.Item>
        <Descriptions.Item label="值">
          <pre class="whitespace-pre-wrap break-all">{{
            detail.value ?? '（敏感项不返回明文）'
          }}</pre>
        </Descriptions.Item>
      </Descriptions>
    </Drawer>

    <Drawer v-model:open="compareOpen" title="文本对比（所选 ↔ 当前）" width="720">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <div class="mb-2 font-medium">所选版本</div>
          <pre class="bg-muted max-h-[70vh] overflow-auto rounded p-3 text-sm">{{
            leftText
          }}</pre>
        </div>
        <div>
          <div class="mb-2 font-medium">当前版本</div>
          <pre class="bg-muted max-h-[70vh] overflow-auto rounded p-3 text-sm">{{
            rightText
          }}</pre>
        </div>
      </div>
    </Drawer>
  </Page>
</template>
