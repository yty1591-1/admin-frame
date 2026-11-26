<script lang="ts" setup>
import type { TableColumnsType, TablePaginationConfig } from 'ant-design-vue';

import type { ScimGroupMapping, ScimToken } from '#/api';

import { onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  message,
  Modal,
  Space,
  Table,
  Tabs,
  Tag,
} from 'ant-design-vue';

import {
  createScimTokenApi,
  listScimGroupsApi,
  listScimTokensApi,
  revokeScimTokenApi,
} from '#/api';

defineOptions({ name: 'IdentityScim' });

const activeTab = ref('tokens');
const tokenLoading = ref(false);
const groupLoading = ref(false);
const issuing = ref(false);
const featureDisabled = ref(false);
const tokens = ref<ScimToken[]>([]);
const groups = ref<ScimGroupMapping[]>([]);
const groupPagination = reactive({
  current: 1,
  pageSize: 20,
  showSizeChanger: true,
  showTotal: (total: number) => `共 ${total} 条`,
  total: 0,
});

const tokenColumns: TableColumnsType = [
  { dataIndex: 'provider', key: 'provider', title: '目录提供方', width: 160 },
  { dataIndex: 'fingerprint', key: 'fingerprint', title: '指纹', width: 240 },
  { dataIndex: 'status', key: 'status', title: '状态', width: 110 },
  { dataIndex: 'created_at', key: 'created_at', title: '创建于', width: 180 },
  { dataIndex: 'expires_at', key: 'expires_at', title: '到期时间', width: 180 },
  { key: 'actions', title: '操作', width: 120 },
];

const groupColumns: TableColumnsType = [
  {
    dataIndex: 'display_name',
    key: 'display_name',
    title: '目录组',
    width: 180,
  },
  {
    dataIndex: 'external_id',
    key: 'external_id',
    title: '外部 ID',
    width: 180,
  },
  {
    dataIndex: 'mapped_role',
    key: 'mapped_role',
    title: '映射角色',
    width: 160,
  },
  {
    dataIndex: 'member_count',
    key: 'member_count',
    title: '成员数',
    width: 100,
  },
  { dataIndex: 'version', key: 'version', title: '版本', width: 90 },
];

const statusMap: Record<string, { color: string; label: string }> = {
  active: { color: 'success', label: '有效' },
  retiring: { color: 'warning', label: '退役中' },
  revoked: { color: 'default', label: '已吊销' },
};

async function fetchTokens() {
  tokenLoading.value = true;
  featureDisabled.value = false;
  try {
    const result = await listScimTokensApi();
    tokens.value = result.items;
  } catch (error) {
    const status = (error as { response?: { status?: number } })?.response
      ?.status;
    if (status === 404) {
      featureDisabled.value = true;
      tokens.value = [];
      message.warning('目录同步未启用（identity.scim.enabled=false）');
    }
  } finally {
    tokenLoading.value = false;
  }
}

async function fetchGroups() {
  groupLoading.value = true;
  try {
    const result = await listScimGroupsApi({
      page: groupPagination.current,
      pageSize: groupPagination.pageSize,
    });
    groups.value = result.items;
    groupPagination.total = result.total;
  } catch (error) {
    const status = (error as { response?: { status?: number } })?.response
      ?.status;
    if (status === 404) {
      featureDisabled.value = true;
      groups.value = [];
      groupPagination.total = 0;
    }
  } finally {
    groupLoading.value = false;
  }
}

function handleGroupTableChange(page: TablePaginationConfig) {
  groupPagination.current = page.current ?? 1;
  groupPagination.pageSize = page.pageSize ?? 20;
  fetchGroups();
}

async function handleIssue() {
  issuing.value = true;
  try {
    const result = await createScimTokenApi();
    await fetchTokens();
    Modal.success({
      content: result.token,
      title: '令牌已签发，请立即保存（仅显示一次）',
      width: 560,
    });
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    issuing.value = false;
  }
}

function handleRevoke(record: ScimToken) {
  Modal.confirm({
    content: `确认吊销指纹 ${record.fingerprint} 的令牌？`,
    onOk: async () => {
      try {
        await revokeScimTokenApi(record.id);
        message.success('令牌已吊销');
        await fetchTokens();
      } catch {
        // 错误已由全局请求拦截器提示
      }
    },
    title: '吊销 SCIM 令牌',
  });
}

onMounted(async () => {
  await Promise.all([fetchTokens(), fetchGroups()]);
});
</script>

<template>
  <Page>
    <div class="bg-card rounded-lg p-4">
      <p v-if="featureDisabled" class="text-muted-foreground mb-3 text-sm">
        当前环境未启用 SCIM（`identity.scim.enabled`）。开启并配置 provider
        后可签发令牌与查看组映射。
      </p>
      <Tabs v-model:active-key="activeTab">
        <Tabs.TabPane key="tokens" tab="令牌管理">
          <Space class="mb-3">
            <Button @click="fetchTokens">刷新</Button>
            <Button
              v-access:code="'identity:scim:write'"
              :loading="issuing"
              type="primary"
              @click="handleIssue"
            >
              签发令牌
            </Button>
          </Space>
          <Table
            :columns="tokenColumns"
            :data-source="tokens"
            :loading="tokenLoading"
            :locale="{
              emptyText: featureDisabled ? '功能未启用' : '暂无数据',
            }"
            :pagination="false"
            row-key="id"
            size="middle"
          >
            <template #bodyCell="{ column, record }">
              <template v-if="column.key === 'status'">
                <Tag :color="statusMap[record.status]?.color ?? 'default'">
                  {{ statusMap[record.status]?.label ?? record.status }}
                </Tag>
              </template>
              <template v-else-if="column.key === 'actions'">
                <Button
                  v-if="record.status !== 'revoked'"
                  v-access:code="'identity:scim:write'"
                  danger
                  size="small"
                  type="link"
                  @click="handleRevoke(record as ScimToken)"
                >
                  吊销
                </Button>
                <span v-else class="text-muted-foreground text-xs">—</span>
              </template>
            </template>
          </Table>
        </Tabs.TabPane>
        <Tabs.TabPane key="groups" tab="组与角色映射">
          <Space class="mb-3">
            <Button @click="fetchGroups">刷新</Button>
          </Space>
          <Table
            :columns="groupColumns"
            :data-source="groups"
            :loading="groupLoading"
            :pagination="groupPagination"
            row-key="id"
            size="middle"
            @change="handleGroupTableChange"
          />
        </Tabs.TabPane>
      </Tabs>
    </div>
  </Page>
</template>
