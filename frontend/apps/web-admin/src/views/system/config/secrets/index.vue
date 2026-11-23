<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type {
  ConfigEnv,
  ConfigSecret,
  ConfigSecretAccess,
  ConfigSecretGrant,
  ConfigSecretVersion,
} from '#/api';

import { h, onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';

import { Page } from '@vben/common-ui';

import {
  Button,
  Drawer,
  Input,
  message,
  Modal,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import { useVbenForm } from '#/adapter/form';
import {
  activateConfigSecretVersionApi,
  approveConfigSecretApi,
  CONFIG_ENV_OPTIONS,
  createConfigSecretApi,
  createConfigSecretGrantApi,
  getConfigErrorMessage,
  listConfigNamespacesApi,
  listConfigSecretAccessesApi,
  listConfigSecretGrantsApi,
  listConfigSecretsApi,
  listConfigSecretVersionsApi,
  revokeConfigSecretApi,
  revokeConfigSecretGrantApi,
  rotateConfigSecretApi,
  submitConfigSecretApi,
} from '#/api';
import { redirectIfInstallRequired } from '#/api/configcenter/ensure-install';

defineOptions({ name: 'SystemConfigSecrets' });

const router = useRouter();

const namespaces = ref<{ label: string; value: string }[]>([]);
const loading = ref(false);
const rows = ref<ConfigSecret[]>([]);
const createOpen = ref(false);
const createSubmitting = ref(false);

const detailOpen = ref(false);
const detailLoading = ref(false);
const detailSecret = ref<ConfigSecret | null>(null);
const detailTab = ref<'accesses' | 'grants' | 'versions'>('versions');
const versions = ref<ConfigSecretVersion[]>([]);
const grants = ref<ConfigSecretGrant[]>([]);
const accesses = ref<ConfigSecretAccess[]>([]);

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
  {
    dataIndex: 'active_version',
    key: 'active_version',
    title: '版本',
    width: 80,
  },
  { dataIndex: 'owner', key: 'owner', title: '负责人', width: 120 },
  { key: 'actions', title: '操作', width: 420 },
];

const versionColumns: TableColumnsType<ConfigSecretVersion> = [
  { dataIndex: 'version', key: 'version', title: '版本', width: 80 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 100 },
  { dataIndex: 'fingerprint', key: 'fingerprint', title: '指纹' },
  { dataIndex: 'created_at', key: 'created_at', title: '创建时间', width: 180 },
  { key: 'actions', title: '操作', width: 120 },
];

const grantColumns: TableColumnsType<ConfigSecretGrant> = [
  { dataIndex: 'service_identity', key: 'service_identity', title: '服务身份' },
  { dataIndex: 'permission', key: 'permission', title: '权限', width: 100 },
  { dataIndex: 'created_at', key: 'created_at', title: '创建时间', width: 180 },
  { dataIndex: 'revoked_at', key: 'revoked_at', title: '吊销时间', width: 180 },
  { key: 'actions', title: '操作', width: 100 },
];

const accessColumns: TableColumnsType<ConfigSecretAccess> = [
  { dataIndex: 'occurred_at', key: 'occurred_at', title: '时间', width: 180 },
  { dataIndex: 'service_identity', key: 'service_identity', title: '服务身份' },
  {
    dataIndex: 'secret_version',
    key: 'secret_version',
    title: '版本',
    width: 80,
  },
  { dataIndex: 'result', key: 'result', title: '结果', width: 120 },
  { dataIndex: 'trace_id', key: 'trace_id', title: 'Trace', width: 160 },
];

async function loadNamespaces() {
  const data = await listConfigNamespacesApi();
  namespaces.value = (data.items ?? []).map((item) => ({
    label: `${item.namespace} (${item.key_count})`,
    value: item.namespace,
  }));
  if (namespaces.value.length === 0) {
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

async function loadDetail(secret: ConfigSecret) {
  detailSecret.value = secret;
  detailOpen.value = true;
  detailLoading.value = true;
  try {
    const [versionData, grantData, accessData] = await Promise.all([
      listConfigSecretVersionsApi(secret.id),
      listConfigSecretGrantsApi(secret.id),
      listConfigSecretAccessesApi(secret.id),
    ]);
    versions.value = versionData.items ?? [];
    grants.value = grantData.items ?? [];
    accesses.value = accessData.items ?? [];
  } catch (error) {
    message.error(getConfigErrorMessage(error, '加载密钥详情失败'));
  } finally {
    detailLoading.value = false;
  }
}

function promptValue(
  title: string,
  placeholder: string,
  help?: string,
): Promise<null | string> {
  return new Promise((resolve) => {
    let value = '';
    Modal.confirm({
      title,
      content: () =>
        h('div', [
          help
            ? h('p', { class: 'text-muted-foreground mb-2 text-xs' }, help)
            : null,
          h(Input, {
            placeholder,
            value,
            'onUpdate:value': (next: string) => {
              value = next;
            },
          }),
        ]),
      okText: '确定',
      cancelText: '取消',
      onCancel: () => resolve(null),
      onOk: () => resolve(value.trim() || null),
    });
  });
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
  const plaintext = await promptValue(
    '提交轮换',
    '新明文（不会回显到列表）',
    '需另一审批人批准后旧版本才进入 grace。',
  );
  if (!plaintext) return;
  try {
    await rotateConfigSecretApi(row.id, { plaintext });
    message.success('已提交轮换，待审批');
    await loadRows();
  } catch (error) {
    message.error(getConfigErrorMessage(error, '提交轮换失败'));
  }
}

async function onRevoke(row: ConfigSecret) {
  Modal.confirm({
    title: '提交吊销',
    content: `确认提交吊销 ${row.name}？需另一审批人批准后生效；旧租约最长存活一个 TTL。`,
    async onOk() {
      await revokeConfigSecretApi(row.id, { comment: 'ui revoke' });
      message.success('已提交吊销，待审批');
      await loadRows();
    },
  });
}

async function onGrant(row: ConfigSecret) {
  const identity = await promptValue(
    '提交授权',
    '服务身份（证书 SAN / SPIFFE ID）',
  );
  if (!identity) return;
  try {
    await createConfigSecretGrantApi(row.id, { service_identity: identity });
    message.success('已提交授权，待审批');
    await loadRows();
    if (detailOpen.value && detailSecret.value?.id === row.id) {
      await loadDetail(row);
    }
  } catch (error) {
    message.error(getConfigErrorMessage(error, '授权失败'));
  }
}

async function onActivateVersion(version: ConfigSecretVersion) {
  const secret = detailSecret.value;
  if (!secret) return;
  if (version.status === 'active') {
    message.info('该版本已是 active');
    return;
  }
  const reason = await promptValue(
    '破玻璃原因',
    '破玻璃原因（必填）',
    '激活历史版本绕过轮换审批，须 24h 内补审。',
  );
  if (!reason) return;
  try {
    await activateConfigSecretVersionApi(secret.id, version.version, {
      break_glass: true,
      break_glass_reason: reason.trim(),
    });
    message.success(`已激活版本 ${version.version}`);
    await loadRows();
    const refreshed = rows.value.find((item) => item.id === secret.id) ?? {
      ...secret,
      active_version: version.version,
    };
    await loadDetail(refreshed);
  } catch (error) {
    message.error(getConfigErrorMessage(error, '激活版本失败'));
  }
}

async function onRevokeGrant(grant: ConfigSecretGrant) {
  const secret = detailSecret.value;
  if (!secret || grant.revoked_at) return;
  Modal.confirm({
    title: '吊销授权',
    content: `确认吊销 ${grant.service_identity}？`,
    async onOk() {
      await revokeConfigSecretGrantApi(secret.id, grant.id);
      message.success('已吊销授权');
      await loadDetail(secret);
    },
  });
}

function onTableChange(pag: TablePaginationConfig) {
  pagination.current = pag.current ?? 1;
  pagination.pageSize = pag.pageSize ?? 20;
  void loadRows();
}

onMounted(async () => {
  if (await redirectIfInstallRequired(router)) {
    return;
  }
  await loadNamespaces();
  await queryFormApi.setFieldValue('namespace', namespaces.value[0]?.value);
  await loadRows();
});
</script>

<template>
  <Page
    description="业务密钥托管：明文一次性输入，列表仅展示指纹。"
    title="密钥管理"
  >
    <div class="mb-4 flex flex-wrap items-end gap-3">
      <QueryForm class="flex-1" />
      <Space>
        <Button type="primary" @click="loadRows">查询</Button>
        <Button
          v-access:code="'config:secret:write'"
          @click="createOpen = true"
        >
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
          <Tag v-if="record.pending_action" color="orange">
            pending:{{ record.pending_action }}
          </Tag>
        </template>
        <template v-else-if="column.key === 'actions'">
          <Space wrap>
            <Button size="small" @click="loadDetail(record as ConfigSecret)">
              详情
            </Button>
            <Button
              v-access:code="'config:secret:write'"
              size="small"
              @click="onSubmit(record as ConfigSecret)"
            >
              送审
            </Button>
            <Button
              v-access:code="'config:secret:approve'"
              size="small"
              @click="onApprove(record as ConfigSecret, 'approved')"
            >
              批准
            </Button>
            <Button
              v-access:code="'config:secret:write'"
              size="small"
              @click="onRotate(record as ConfigSecret)"
            >
              轮换
            </Button>
            <Button
              v-access:code="'config:secret:write'"
              size="small"
              @click="onGrant(record as ConfigSecret)"
            >
              授权
            </Button>
            <Button
              v-access:code="'config:secret:write'"
              danger
              size="small"
              @click="onRevoke(record as ConfigSecret)"
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

    <Drawer
      v-model:open="detailOpen"
      :title="detailSecret ? `密钥详情 · ${detailSecret.name}` : '密钥详情'"
      width="720"
    >
      <Space class="mb-3">
        <Button
          :type="detailTab === 'versions' ? 'primary' : 'default'"
          size="small"
          @click="detailTab = 'versions'"
        >
          版本
        </Button>
        <Button
          :type="detailTab === 'grants' ? 'primary' : 'default'"
          size="small"
          @click="detailTab = 'grants'"
        >
          授权
        </Button>
        <Button
          :type="detailTab === 'accesses' ? 'primary' : 'default'"
          size="small"
          @click="detailTab = 'accesses'"
        >
          访问审计
        </Button>
      </Space>

      <Table
        v-if="detailTab === 'versions'"
        :columns="versionColumns"
        :data-source="versions"
        :loading="detailLoading"
        :pagination="false"
        row-key="version"
        size="small"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'actions'">
            <Button
              v-access:code="'config:secret:write'"
              :disabled="
                record.status === 'active' || record.status === 'destroyed'
              "
              size="small"
              @click="onActivateVersion(record as ConfigSecretVersion)"
            >
              激活
            </Button>
          </template>
        </template>
      </Table>

      <Table
        v-else-if="detailTab === 'grants'"
        :columns="grantColumns"
        :data-source="grants"
        :loading="detailLoading"
        :pagination="false"
        row-key="id"
        size="small"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'actions'">
            <Button
              v-access:code="'config:secret:write'"
              :disabled="!!record.revoked_at"
              danger
              size="small"
              @click="onRevokeGrant(record as ConfigSecretGrant)"
            >
              吊销
            </Button>
          </template>
        </template>
      </Table>

      <Table
        v-else
        :columns="accessColumns"
        :data-source="accesses"
        :loading="detailLoading"
        :pagination="false"
        row-key="trace_id"
        size="small"
      />
    </Drawer>
  </Page>
</template>
