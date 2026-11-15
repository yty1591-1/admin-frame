<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { ConfigEnv, ConfigSecret } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import { Button, message, Modal, Space, Table, Tag } from 'ant-design-vue';

import { useVbenForm } from '#/adapter/form';
import {
  CONFIG_ENV_OPTIONS,
  approveConfigSecretApi,
  createConfigSecretApi,
  createConfigSecretGrantApi,
  getConfigErrorMessage,
  listConfigNamespacesApi,
  listConfigSecretsApi,
  revokeConfigSecretApi,
  rotateConfigSecretApi,
  submitConfigSecretApi,
} from '#/api';

defineOptions({ name: 'SystemConfigSecrets' });

const namespaces = ref<{ label: string; value: string }[]>([]);
const loading = ref(false);
const rows = ref<ConfigSecret[]>([]);
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
      componentProps: { options: CONFIG_ENV_OPTIONS, placeholder: '环境' },
      defaultValue: 'dev',
      fieldName: 'env',
      label: '环境',
      rules: 'required',
    },
    {
      component: 'Select',
      componentProps: {
        options: namespaces,
        placeholder: '命名空间',
        showSearch: true,
      },
      fieldName: 'namespace',
      label: '命名空间',
      rules: 'required',
    },
  ],
  showDefaultActions: false,
  commonConfig: { labelWidth: 80 },
  wrapperClass: 'grid-cols-1 md:grid-cols-3',
});

const [CreateForm, createFormApi] = useVbenForm({
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
      componentProps: { options: namespaces, showSearch: true },
      fieldName: 'namespace',
      label: '命名空间',
      rules: 'required',
    },
    {
      component: 'Input',
      fieldName: 'name',
      label: '密钥名',
      rules: 'required',
    },
    {
      component: 'Input',
      fieldName: 'owner',
      label: '负责人',
      rules: 'required',
    },
    {
      component: 'InputPassword',
      fieldName: 'plaintext',
      label: '明文（一次性）',
      rules: 'required',
    },
    {
      component: 'Input',
      fieldName: 'description',
      label: '说明',
    },
  ],
  showDefaultActions: false,
  commonConfig: { labelWidth: 120 },
});

const columns: TableColumnsType<ConfigSecret> = [
  { dataIndex: 'name', key: 'name', title: '名称', width: 220 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 120 },
  { dataIndex: 'fingerprint', key: 'fingerprint', title: '指纹', width: 160 },
  { dataIndex: 'active_version', key: 'active_version', title: '版本', width: 80 },
  { dataIndex: 'owner', key: 'owner', title: '负责人', width: 120 },
  { key: 'actions', title: '操作', width: 360 },
];

async function loadNamespaces() {
  const data = await listConfigNamespacesApi();
  namespaces.value = (data.items ?? []).map((item) => ({
    label: `${item.namespace} (${item.key_count})`,
    value: item.namespace,
  }));
  if (!namespaces.value.length) {
    namespaces.value = [{ label: 'platform', value: 'platform' }];
  }
}

async function loadRows() {
  const values = await queryFormApi.getValues();
  loading.value = true;
  try {
    const data = await listConfigSecretsApi({
      env: values.env as ConfigEnv,
      namespace: values.namespace,
      page: pagination.current,
      page_size: pagination.pageSize,
    });
    rows.value = data.items ?? [];
    pagination.total = data.total ?? 0;
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载密钥失败'));
  } finally {
    loading.value = false;
  }
}

async function onCreate() {
  const { valid } = await createFormApi.validate();
  if (!valid) return;
  createSubmitting.value = true;
  try {
    const values = await createFormApi.getValues();
    await createConfigSecretApi(values);
    message.success('已创建（明文不会回显）');
    createOpen.value = false;
    createFormApi.resetForm();
    await loadRows();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '创建失败'));
  } finally {
    createSubmitting.value = false;
  }
}

async function onSubmit(row: ConfigSecret) {
  try {
    await submitConfigSecretApi(row.id);
    message.success('已送审');
    await loadRows();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '送审失败'));
  }
}

async function onApprove(row: ConfigSecret, decision: 'approved' | 'rejected') {
  try {
    await approveConfigSecretApi(row.id, { decision });
    message.success(decision === 'approved' ? '已批准' : '已驳回');
    await loadRows();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '审批失败'));
  }
}

async function onRotate(row: ConfigSecret) {
  Modal.confirm({
    title: '轮换密钥',
    content: '请在下一提示中输入新明文；旧版本将进入 grace。',
    async onOk() {
      const plaintext = window.prompt('新明文（不会回显到列表）');
      if (!plaintext) return;
      await rotateConfigSecretApi(row.id, { plaintext });
      message.success('已轮换');
      await loadRows();
    },
  });
}

async function onRevoke(row: ConfigSecret) {
  Modal.confirm({
    title: '吊销密钥',
    content: `确认吊销 ${row.name}？旧租约最长存活一个 TTL。`,
    async onOk() {
      await revokeConfigSecretApi(row.id, { comment: 'ui revoke' });
      message.success('已吊销');
      await loadRows();
    },
  });
}

async function onGrant(row: ConfigSecret) {
  const identity = window.prompt('服务身份（证书 SAN / SPIFFE ID）');
  if (!identity) return;
  try {
    await createConfigSecretGrantApi(row.id, { service_identity: identity });
    message.success('已授权');
  } catch (error) {
    message.error(getConfigErrorMessage(error, '授权失败'));
  }
}

function onTableChange(pag: TablePaginationConfig) {
  pagination.current = pag.current ?? 1;
  pagination.pageSize = pag.pageSize ?? 20;
  void loadRows();
}

onMounted(async () => {
  await loadNamespaces();
  await queryFormApi.setFieldValue('namespace', namespaces.value[0]?.value);
  await loadRows();
});
</script>

<template>
  <Page description="业务密钥托管：明文一次性输入，列表仅展示指纹。" title="密钥管理">
    <div class="mb-4 flex flex-wrap items-end gap-3">
      <QueryForm class="flex-1" />
      <Space>
        <Button type="primary" @click="loadRows">查询</Button>
        <Button v-access:code="'config:secret:write'" @click="createOpen = true">
          新建密钥
        </Button>
      </Space>
    </div>

    <Table
      :columns="columns"
      :data-source="rows"
      :loading="loading"
      :pagination="pagination"
      row-key="id"
      @change="onTableChange"
    >
      <template #bodyCell="{ column, record }">
        <template v-if="column.key === 'status'">
          <Tag>{{ record.status }}</Tag>
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space wrap>
            <Button
              v-access:code="'config:secret:write'"
              size="small"
              @click="onSubmit(record)"
            >
              送审
            </Button>
            <Button
              v-access:code="'config:secret:approve'"
              size="small"
              @click="onApprove(record, 'approved')"
            >
              批准
            </Button>
            <Button
              v-access:code="'config:secret:write'"
              size="small"
              @click="onRotate(record)"
            >
              轮换
            </Button>
            <Button
              v-access:code="'config:secret:write'"
              size="small"
              @click="onGrant(record)"
            >
              授权
            </Button>
            <Button
              v-access:code="'config:secret:write'"
              danger
              size="small"
              @click="onRevoke(record)"
            >
              吊销
            </Button>
          </Space>
        </template>
      </template>
    </Table>

    <Modal
      v-model:open="createOpen"
      :confirm-loading="createSubmitting"
      title="新建业务密钥"
      @ok="onCreate"
    >
      <CreateForm />
      <p class="mt-2 text-sm text-gray-500">
        明文仅提交一次，之后无法查看；请确认已安全交接给使用方。
      </p>
    </Modal>
  </Page>
</template>
