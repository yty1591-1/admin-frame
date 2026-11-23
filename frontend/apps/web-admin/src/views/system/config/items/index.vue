<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { ConfigEnv, ConfigItem, ConfigItemStatus } from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';

import { Page } from '@vben/common-ui';

import { Button, message, Modal, Space, Table, Tag } from 'ant-design-vue';

import { useVbenForm } from '#/adapter/form';
import {
  CONFIG_ENV_OPTIONS,
  CONFIG_ITEM_STATUS_OPTIONS,
  createConfigItemApi,
  getConfigErrorMessage,
  listConfigDeclaredKeysApi,
  listConfigItemsApi,
  listConfigNamespacesApi,
} from '#/api';
import { redirectIfInstallRequired } from '#/api/configcenter/ensure-install';

defineOptions({ name: 'SystemConfigItems' });

const router = useRouter();

const namespaces = ref<{ label: string; value: string }[]>([]);
const declaredKeyOptions = ref<{ label: string; value: string }[]>([]);
const loading = ref(false);
const rows = ref<ConfigItem[]>([]);
const createOpen = ref(false);
const createSubmitting = ref(false);

const pagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const [QueryForm, queryFormApi] = useVbenForm({
  schema: [
    {
      component: 'Select',
      componentProps: {
        options: CONFIG_ENV_OPTIONS,
        placeholder: '环境',
      },
      defaultValue: 'dev',
      fieldName: 'env',
      label: '环境',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: false,
        options: namespaces,
        placeholder: '命名空间',
        showSearch: true,
      },
      fieldName: 'namespace',
      label: '命名空间',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: CONFIG_ITEM_STATUS_OPTIONS,
        placeholder: '状态',
      },
      fieldName: 'status',
      label: '状态',
    },
    {
      component: 'Input',
      componentProps: {
        allowClear: true,
        placeholder: '按 key 过滤（前端过滤）',
      },
      fieldName: 'keyword',
      label: '关键字',
    },
  ],
  showDefaultActions: false,
});

const [CreateForm, createFormApi] = useVbenForm({
  commonConfig: { componentProps: { class: 'w-full' } },
  schema: [
    {
      component: 'Select',
      componentProps: { options: CONFIG_ENV_OPTIONS },
      defaultValue: 'dev',
      fieldName: 'env',
      label: '环境',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: {
        options: namespaces,
        showSearch: true,
      },
      fieldName: 'namespace',
      label: '命名空间',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: {
        options: declaredKeyOptions,
        placeholder: '从代码声明键选择（排除敏感键）',
        showSearch: true,
      },
      fieldName: 'key',
      label: '配置键',
      rules: 'required',
    },
    {
      component: 'Input',
      componentProps: { placeholder: '可选初始值；敏感键勿填' },
      fieldName: 'value',
      label: '初始值',
    },
    {
      component: 'Input',
      fieldName: 'description',
      label: '说明',
    },
    {
      component: 'Input',
      fieldName: 'owner',
      label: '负责人',
    },
    {
      component: 'Input',
      fieldName: 'change_summary',
      label: '变更摘要',
    },
  ],
  showDefaultActions: false,
});

const columns: TableColumnsType = [
  { dataIndex: 'key', key: 'key', title: '配置键', width: 240 },
  { dataIndex: 'value_type', key: 'value_type', title: '类型', width: 100 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 100 },
  {
    dataIndex: 'current_version',
    key: 'current_version',
    title: '版本',
    width: 80,
  },
  { dataIndex: 'owner', key: 'owner', title: '负责人', width: 120 },
  { dataIndex: 'sensitive', key: 'sensitive', title: '敏感', width: 80 },
  { dataIndex: 'updated_at', key: 'updated_at', title: '更新时间', width: 190 },
  { key: 'actions', title: '操作', width: 220 },
];

const statusColor = computed(() => {
  return {
    active: 'success',
    archived: 'default',
    deprecated: 'warning',
    draft: 'orange',
  } as Record<ConfigItemStatus, string>;
});

async function loadNamespaces() {
  const result = await listConfigNamespacesApi();
  namespaces.value = result.items.map((item) => ({
    label: `${item.namespace} (${item.key_count})`,
    value: item.namespace,
  }));
  if (namespaces.value.length > 0) {
    const current = await queryFormApi.getValues();
    if (!current.namespace) {
      await queryFormApi.setValues({ namespace: namespaces.value[0]?.value });
    }
  }
}

async function fetchList() {
  const values = await queryFormApi.getValues();
  if (!values.env || !values.namespace) {
    message.warning('请选择环境与命名空间');
    return;
  }
  loading.value = true;
  try {
    const result = await listConfigItemsApi({
      env: values.env as ConfigEnv,
      namespace: values.namespace,
      page: pagination.current,
      pageSize: pagination.pageSize,
      status: (values.status || undefined) as ConfigItemStatus | undefined,
    });
    const keyword = String(values.keyword || '')
      .trim()
      .toLowerCase();
    rows.value = keyword
      ? result.items.filter((item) => item.key.toLowerCase().includes(keyword))
      : result.items;
    pagination.total = keyword ? rows.value.length : result.total;
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载配置列表失败'));
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
  if (namespaces.value[0]) {
    await queryFormApi.setValues({
      env: 'dev',
      namespace: namespaces.value[0].value,
    });
  }
  handleSearch();
}

function handleTableChange(page: TablePaginationConfig) {
  pagination.current = page.current ?? 1;
  pagination.pageSize = page.pageSize ?? 20;
  fetchList();
}

async function openCreate() {
  const values = await queryFormApi.getValues();
  const namespace = String(
    values.namespace || namespaces.value[0]?.value || '',
  );
  createOpen.value = true;
  await createFormApi.reset();
  await createFormApi.setValues({
    env: values.env || 'dev',
    namespace,
  });
  declaredKeyOptions.value = [];
  if (!namespace) {
    return;
  }
  try {
    const declared = await listConfigDeclaredKeysApi(namespace);
    const existing = new Set(rows.value.map((item) => item.key));
    declaredKeyOptions.value = declared.items
      .filter((item) => !item.sensitive && !existing.has(item.key))
      .map((item) => ({
        label: `${item.key} (${item.value_type}${item.reload_mode === 'hot' ? ', hot' : ''})`,
        value: item.key,
      }));
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载声明键失败'));
  }
}

async function submitCreate() {
  const { valid } = await createFormApi.validate();
  if (!valid) {
    return;
  }
  const values = await createFormApi.getValues();
  createSubmitting.value = true;
  try {
    await createConfigItemApi({
      change_summary: values.change_summary || undefined,
      description: values.description || undefined,
      env: values.env,
      key: values.key,
      namespace: values.namespace,
      owner: values.owner || undefined,
      value: values.value || undefined,
    });
    message.success('已创建配置键');
    createOpen.value = false;
    fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '创建失败'));
  } finally {
    createSubmitting.value = false;
  }
}

function goEdit(record: ConfigItem) {
  router.push({
    path: '/system/config/edit',
    query: { id: String(record.id) },
  });
}

function goVersions(record: ConfigItem) {
  router.push({
    path: '/system/config/versions',
    query: { id: String(record.id) },
  });
}

onMounted(async () => {
  if (await redirectIfInstallRequired(router)) {
    return;
  }
  try {
    await loadNamespaces();
    await fetchList();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '初始化失败'));
  }
});
</script>

<template>
  <Page
    description="按环境与命名空间浏览配置草稿；敏感项不展示明文，编辑与版本走独立页"
    title="配置列表"
  >
    <div class="bg-card mb-4 rounded-lg p-4">
      <QueryForm />
      <Space class="mt-2">
        <Button type="primary" @click="handleSearch">查询</Button>
        <Button @click="handleReset">重置</Button>
        <Button
          v-access:code="'config:write'"
          type="primary"
          @click="openCreate"
        >
          新建配置键
        </Button>
      </Space>
    </div>

    <Table
      :columns="columns"
      :data-source="rows"
      :loading="loading"
      :pagination="pagination"
      :scroll="{ x: 1200 }"
      row-key="id"
      @change="handleTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'status'">
          <Tag :color="statusColor[record.status as ConfigItemStatus]">
            {{ record.status }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'sensitive'">
          <Tag :color="record.sensitive ? 'error' : 'default'">
            {{ record.sensitive ? '是' : '否' }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space>
            <Button
              v-access:code="'config:write'"
              :disabled="record.sensitive"
              size="small"
              type="link"
              @click="goEdit(record as ConfigItem)"
            >
              编辑
            </Button>
            <Button
              size="small"
              type="link"
              @click="goVersions(record as ConfigItem)"
            >
              版本
            </Button>
          </Space>
        </template>
      </template>
    </Table>

    <Modal
      v-model:open="createOpen"
      :confirm-loading="createSubmitting"
      title="新建配置键"
      width="640px"
      @ok="submitCreate"
    >
      <CreateForm />
    </Modal>
  </Page>
</template>
