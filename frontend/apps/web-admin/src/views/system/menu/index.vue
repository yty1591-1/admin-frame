<script lang="ts" setup>
import type { TableColumnsType } from 'ant-design-vue';

import type { FormField } from '../../iam/components/types';

import type { MenuAdmin, MenuAdminTreeNode } from '#/api';

import { computed, onMounted, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  message,
  Modal,
  Space,
  Switch,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  createMenuApi,
  disableMenuApi,
  enableMenuApi,
  getMenuTreeApi,
  listAllPermissionsApi,
  listMenuComponentsApi,
  updateMenuApi,
} from '#/api';

import FormModal from '../../iam/components/form-modal.vue';

defineOptions({ name: 'SystemMenu' });

const loading = ref(false);
const includeDisabled = ref(true);
const tree = ref<MenuAdminTreeNode[]>([]);
const components = ref<string[]>([]);
const permissionOptions = ref<{ label: string; value: string }[]>([]);

const formOpen = ref(false);
const formSubmitting = ref(false);
const editing = ref<MenuAdmin>();

const columns: TableColumnsType = [
  { dataIndex: 'title', key: 'title', title: '名称', width: 180 },
  { dataIndex: 'code', key: 'code', title: '编码', width: 180 },
  { dataIndex: 'route_path', key: 'route_path', title: '路由', width: 180 },
  { dataIndex: 'component', key: 'component', title: '组件', width: 220 },
  {
    dataIndex: 'permission_code',
    key: 'permission_code',
    title: '权限码',
    width: 180,
  },
  { dataIndex: 'status', key: 'status', title: '状态', width: 90 },
  { dataIndex: 'sort', key: 'sort', title: '排序', width: 80 },
  { key: 'actions', title: '操作', width: 220 },
];

function flatten(nodes: MenuAdminTreeNode[]): MenuAdminTreeNode[] {
  const out: MenuAdminTreeNode[] = [];
  for (const node of nodes) {
    out.push(node);
    if (node.children?.length) {
      out.push(...flatten(node.children));
    }
  }
  return out;
}

const flatMenus = computed(() => flatten(tree.value));
const parentOptions = computed(() =>
  flatMenus.value
    .filter(
      (item) => item.id !== editing.value?.id && item.node_type === 'catalog',
    )
    .map((item) => ({
      label: `${item.title}（${item.code}）`,
      value: item.code,
    })),
);

const formFields = computed<FormField[]>(() => [
  {
    disabled: !!editing.value,
    label: '编码',
    name: 'code',
    required: !editing.value,
  },
  {
    component: 'select',
    label: '父目录',
    name: 'parent_code',
    options: parentOptions.value,
    placeholder: '根节点留空',
  },
  {
    component: 'select',
    label: '节点类型',
    name: 'node_type',
    options: [
      { label: '目录', value: 'catalog' },
      { label: '菜单', value: 'menu' },
      { label: 'iframe', value: 'iframe' },
      { label: '外链', value: 'link' },
    ],
    required: true,
  },
  { label: '路由名称', name: 'route_name', required: true },
  { label: '路由路径', name: 'route_path', required: true },
  {
    component: 'select',
    label: '页面组件',
    name: 'component',
    options: components.value.map((item) => ({ label: item, value: item })),
    placeholder: '目录 / 外链可留空',
  },
  { label: '重定向', name: 'redirect' },
  { label: '显示名称', name: 'title', required: true },
  { label: 'i18n Key', name: 'title_key' },
  { label: '图标', name: 'icon', placeholder: '如 lucide:shield' },
  {
    component: 'select',
    label: '可见权限码',
    name: 'permission_code',
    options: permissionOptions.value,
    placeholder: '空表示登录即可见',
  },
  {
    component: 'number',
    label: '排序',
    max: 9999,
    min: 0,
    name: 'sort',
  },
  {
    component: 'switch',
    label: '菜单中隐藏',
    name: 'hide_in_menu',
  },
  {
    component: 'switch',
    label: 'KeepAlive',
    name: 'keep_alive',
  },
  { label: '外链地址', name: 'external_url', span: 2 },
  {
    component: 'textarea',
    label: '变更原因',
    name: 'reason',
    rows: 2,
    span: 2,
  },
]);

async function fetchTree() {
  loading.value = true;
  try {
    tree.value = await getMenuTreeApi(includeDisabled.value);
  } finally {
    loading.value = false;
  }
}

async function loadOptions() {
  const [componentList, permissions] = await Promise.all([
    listMenuComponentsApi(),
    listAllPermissionsApi(),
  ]);
  components.value = componentList;
  permissionOptions.value = permissions.map((item) => ({
    label: `${item.name}（${item.code}）`,
    value: item.code,
  }));
}

function openCreate() {
  editing.value = undefined;
  formOpen.value = true;
}

function openEdit(menu: MenuAdmin) {
  editing.value = menu;
  formOpen.value = true;
}

async function handleSubmit(payload: Record<string, any>) {
  formSubmitting.value = true;
  const common = {
    component: payload.component,
    external_url: payload.external_url,
    hide_in_menu: payload.hide_in_menu,
    icon: payload.icon,
    keep_alive: payload.keep_alive,
    node_type: payload.node_type,
    parent_code: payload.parent_code,
    permission_code: payload.permission_code,
    reason: payload.reason,
    redirect: payload.redirect,
    route_name: payload.route_name,
    route_path: payload.route_path,
    sort: payload.sort,
    title: payload.title,
    title_key: payload.title_key,
  };
  try {
    await (editing.value
      ? updateMenuApi(editing.value.id, {
          ...common,
          version: editing.value.version,
        })
      : createMenuApi({ ...common, code: payload.code }));
    message.success('菜单已保存；刷新页面后生效');
    formOpen.value = false;
    await fetchTree();
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    formSubmitting.value = false;
  }
}

function toggleMenu(menu: MenuAdmin) {
  const enabling = menu.status === 'disabled';
  Modal.confirm({
    content: enabling
      ? '启用后该入口会在有权限的账号侧边栏出现。'
      : '停用只隐藏入口，不删除菜单与子节点。',
    okText: enabling ? '启用' : '停用',
    onOk: async () => {
      try {
        await (enabling
          ? enableMenuApi(menu.id, {
              reason: '页面启用',
              version: menu.version,
            })
          : disableMenuApi(menu.id, {
              reason: '页面停用',
              version: menu.version,
            }));
        message.success('菜单状态已更新；刷新页面后生效');
        await fetchTree();
      } catch {
        // 错误已由全局请求拦截器提示
      }
    },
    title: enabling ? '启用菜单' : '停用菜单',
  });
}

onMounted(async () => {
  await Promise.all([fetchTree(), loadOptions()]);
});
</script>

<template>
  <Page>
    <div class="bg-card rounded-lg border p-4">
      <Space class="mb-3">
        <span class="text-muted-foreground text-sm">包含停用节点</span>
        <Switch v-model:checked="includeDisabled" @change="fetchTree" />
        <Button @click="fetchTree">刷新</Button>
        <Button v-access:code="'iam:menu:write'" @click="openCreate">
          新建菜单
        </Button>
      </Space>

      <Table
        :columns="columns"
        :data-source="tree"
        :loading="loading"
        :pagination="false"
        default-expand-all-rows
        row-key="id"
        size="middle"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <Tag :color="record.status === 'active' ? 'success' : 'default'">
              {{ record.status === 'active' ? '启用' : '停用' }}
            </Tag>
          </template>
          <template v-else-if="column.key === 'actions'">
            <Space :size="4">
              <Button
                v-access:code="'iam:menu:write'"
                size="small"
                type="link"
                @click="openEdit(record as MenuAdmin)"
              >
                编辑
              </Button>
              <Button
                v-access:code="'iam:menu:write'"
                size="small"
                type="link"
                @click="toggleMenu(record as MenuAdmin)"
              >
                {{ record.status === 'active' ? '停用' : '启用' }}
              </Button>
            </Space>
          </template>
        </template>
      </Table>
    </div>

    <FormModal
      v-model:open="formOpen"
      :fields="formFields"
      :loading="formSubmitting"
      :model-value="editing ?? { node_type: 'menu', sort: 0 }"
      :title="editing ? '编辑菜单' : '新建菜单'"
      :width="760"
      @submit="handleSubmit"
    />
  </Page>
</template>
