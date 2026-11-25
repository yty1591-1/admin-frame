<script lang="ts" setup>
import type { FieldSection, InstallDraft } from './schema';

import type {
  ApplyResult,
  InstallGap,
  InstallMode,
  InstallSchema,
  InstallStatusData,
  SchemaField,
} from '#/api/configcenter/install';

import { computed, onMounted, reactive, ref } from 'vue';
import { useRouter } from 'vue-router';

import { Page } from '@vben/common-ui';

import {
  Alert,
  Button,
  Input,
  InputNumber,
  Select,
  Space,
  Steps,
  Switch,
  Tag,
} from 'ant-design-vue';

import {
  applyInstall,
  beginInstall,
  completeInstall,
  fetchInstallSchema,
  fetchInstallStatus,
} from '#/api/configcenter/install';

import {
  buildSections,
  collectValues,
  configFields,
  declaredKeyRefs,
  defaultBadge,
  filledCount,
  missingFields,
  secretFields,
} from './schema';

const router = useRouter();

const step = ref(0);
const token = ref('');
const owner = ref('');
const declaredText = ref('');
const busy = ref(false);
const message = ref('');
const errorMessage = ref('');

const status = ref<InstallStatusData | null>(null);
const schema = ref<InstallSchema | null>(null);
const draft = reactive<InstallDraft>({});
const remainingGaps = ref<InstallGap[]>([]);

const mode = computed<InstallMode>(() => schema.value?.mode ?? 'first_install');
const isRepair = computed(() => mode.value === 'repair');

const formFields = computed<SchemaField[]>(() =>
  schema.value ? configFields(schema.value, mode.value) : [],
);
const secrets = computed<SchemaField[]>(() =>
  schema.value ? secretFields(schema.value) : [],
);
const namespaceTitles = computed<Record<string, string>>(() => {
  const titles: Record<string, string> = {};
  for (const item of schema.value?.namespaces ?? []) {
    titles[item.namespace] = item.title || item.namespace;
  }
  return titles;
});
const sections = computed<FieldSection[]>(() =>
  buildSections(formFields.value, namespaceTitles.value),
);

/** 平台/基础设施域放第 2 步，业务域放第 3 步；repair 时只会有阻塞键。 */
const platformSections = computed<FieldSection[]>(() =>
  sections.value.filter((section) => section.namespace === 'platform'),
);
const businessSections = computed<FieldSection[]>(() =>
  sections.value.filter((section) => section.namespace !== 'platform'),
);

const blockingCount = computed(
  () => schema.value?.blocking_count ?? status.value?.blocking_count ?? 0,
);
const gaps = computed<InstallGap[]>(() => {
  if (remainingGaps.value.length > 0) {
    return remainingGaps.value;
  }
  return status.value?.gaps ?? [];
});
const missingRequired = computed(() =>
  missingFields(formFields.value, draft, mode.value),
);

const canSubmit = computed(
  () =>
    !busy.value &&
    !!token.value &&
    !!schema.value &&
    missingRequired.value.length === 0,
);

async function refreshStatus() {
  errorMessage.value = '';
  try {
    status.value = await fetchInstallStatus();
    if (status.value.status === 'ready') {
      message.value = '安装已完成，可返回登录。';
    }
  } catch (error) {
    errorMessage.value = describe(error);
  }
}

async function loadSchema() {
  if (!token.value) {
    errorMessage.value = '先填 Setup Token 再加载配置清单。';
    return;
  }
  busy.value = true;
  errorMessage.value = '';
  message.value = '';
  try {
    schema.value = await fetchInstallSchema(token.value);
    remainingGaps.value = [];
    message.value =
      schema.value.mode === 'repair'
        ? `进入 repair 模式：仅阻塞键可改（${schema.value.blocking_count} 个）`
        : `配置清单已加载：${schema.value.fields.length} 项 / ${schema.value.namespaces.length} 个命名空间`;
    step.value = 1;
  } catch (error) {
    errorMessage.value = describe(error);
  } finally {
    busy.value = false;
  }
}

async function runInstall() {
  if (missingRequired.value.length > 0) {
    errorMessage.value = `还有必填项未填：${missingRequired.value
      .map((field) => field.label || field.key)
      .join('、')}`;
    return;
  }
  busy.value = true;
  errorMessage.value = '';
  message.value = '';
  try {
    const values = collectValues(formFields.value, draft);
    const begun = await beginInstall(token.value, {
      declaredKeys: isRepair.value
        ? declaredKeyRefs(declaredText.value)
        : undefined,
      owner: owner.value || undefined,
      reason: mode.value,
    });
    owner.value = begun.owner || owner.value;

    const applied: ApplyResult = await applyInstall(token.value, {
      owner: owner.value,
      values,
    });
    remainingGaps.value = applied.remaining_gaps;
    message.value = `已写入 ${applied.item_count} 个键 / ${applied.namespaces} 个命名空间`;

    if (applied.remaining_gaps.length > 0) {
      errorMessage.value = `仍有 ${applied.remaining_gaps.length} 个阻塞键未满足，补齐后再提交（无需重新 begin）。`;
      step.value = 4;
      return;
    }
    const done = await completeInstall(token.value, owner.value);
    status.value = { ...done, blocking_count: 0, gaps: [] };
    message.value = '安装完成（status=ready）';
    step.value = 4;
  } catch (error) {
    errorMessage.value = describe(error);
    await refreshStatus();
  } finally {
    busy.value = false;
  }
}

function fieldValue(field: SchemaField): string {
  return draft[field.namespace]?.[field.key] ?? '';
}

function setFieldValue(field: SchemaField, value: boolean | number | string) {
  draft[field.namespace] ??= {};
  const group = draft[field.namespace];
  if (!group) {
    return;
  }
  group[field.key] =
    typeof value === 'boolean' ? String(value) : String(value ?? '');
}

function inputValue(event: Event): string {
  const target = event.target as HTMLInputElement | null;
  return target?.value ?? '';
}

function isOn(field: SchemaField): boolean {
  return fieldValue(field) === 'true';
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isFieldFilled(field: SchemaField): boolean {
  return (draft[field.namespace]?.[field.key] ?? '').trim() !== '';
}

onMounted(refreshStatus);
</script>

<template>
  <Page
    title="配置中心安装向导"
    description="ADR-0018：所有环境统一经安装向导播种；表单由 /install/schema 驱动，repair 模式只渲染阻塞键"
  >
    <div class="mx-auto max-w-5xl space-y-6 p-6">
      <Steps
        :current="step"
        size="small"
        :items="[
          { title: '环境与令牌' },
          { title: '平台与基础设施' },
          { title: '业务配置' },
          { title: '业务密钥' },
          { title: '预览与提交' },
        ]"
      />

      <div class="flex flex-wrap items-center gap-3 text-sm">
        <Tag :color="status?.status === 'ready' ? 'green' : 'orange'">
          状态：{{ status?.status || '—' }}
        </Tag>
        <Tag>环境：{{ status?.env || schema?.env || '—' }}</Tag>
        <Tag :color="isRepair ? 'blue' : 'default'">
          模式：{{ isRepair ? 'repair（重配置）' : 'first_install（首装）' }}
        </Tag>
        <Tag v-if="blockingCount > 0" color="red">
          阻塞键：{{ blockingCount }}
        </Tag>
        <span v-if="status?.owner" class="text-slate-500">
          持锁：{{ status.owner }}
        </span>
        <span v-if="status?.last_error" class="text-red-600">
          上次失败：{{ status.last_error }}
        </span>
      </div>

      <Alert
        v-if="gaps.length > 0"
        type="warning"
        show-icon
        :message="`${gaps.length} 个必填键缺失，进程无法完成启动`"
      >
        <template #description>
          <ul class="list-disc pl-4">
            <li
              v-for="gap in gaps.slice(0, 8)"
              :key="`${gap.namespace}/${gap.key}`"
            >
              <code>{{ gap.namespace }}/{{ gap.key }}</code>
              <span v-if="gap.reason" class="text-slate-500">
                （{{ gap.reason }}）
              </span>
            </li>
            <li v-if="gaps.length > 8">…共 {{ gaps.length }} 项</li>
          </ul>
        </template>
      </Alert>

      <!-- 第 1 步：令牌与环境 -->
      <section v-show="step === 0" class="space-y-4">
        <label class="block text-sm">
          Setup Token
          <Input
            v-model:value="token"
            type="password"
            autocomplete="off"
            placeholder="CONFIG_INSTALL_SETUP_TOKEN"
          />
          <span class="text-xs text-slate-500">
            写操作与 schema 都要它；不落库、不回显，浏览器不直连 config-api。
          </span>
        </label>

        <label class="block text-sm">
          Owner（可选，begin 后回填）
          <Input v-model:value="owner" placeholder="例如：运维值班组" />
        </label>

        <label v-if="isRepair" class="block text-sm">
          声明的阻塞键（可选，逐行或逗号分隔的
          <code>namespace/key</code>）
          <Input.TextArea
            v-model:value="declaredText"
            :rows="3"
            placeholder="platform/auth.jwt.secret"
          />
          <span class="text-xs text-slate-500">
            用于「服务端推导不出阻塞键、但进程确实起不来」的场景；键必须已声明、非敏感、且尚未在生效快照里有值。
          </span>
        </label>

        <Space>
          <Button type="primary" :loading="busy" @click="loadSchema">
            加载配置清单
          </Button>
          <Button :disabled="busy" @click="refreshStatus">刷新状态</Button>
          <Button @click="router.push('/auth/login')">去登录</Button>
        </Space>
      </section>

      <!-- 第 2 步：平台与基础设施 -->
      <section v-show="step === 1" class="space-y-6">
        <Alert
          v-if="isRepair && platformSections.length === 0"
          type="info"
          show-icon
          message="repair 模式下该域没有阻塞键，无需修改"
        />
        <div
          v-for="section in platformSections"
          :key="section.namespace"
          class="space-y-4 rounded border p-4"
        >
          <header class="flex items-center gap-2">
            <h3 class="text-base font-semibold">{{ section.title }}</h3>
            <Tag v-if="section.blockingCount > 0" color="red">
              阻塞 {{ section.blockingCount }}
            </Tag>
          </header>
          <div
            v-for="group in section.groups"
            :key="group.id"
            class="space-y-3"
          >
            <h4 class="text-sm font-medium text-slate-600">
              {{ group.title }}
            </h4>
            <div
              v-for="field in group.fields"
              :key="field.key"
              class="grid grid-cols-1 gap-1 border-b pb-3 last:border-0 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]"
            >
              <div class="space-y-1">
                <div class="flex flex-wrap items-center gap-2 text-sm">
                  <span class="font-medium">{{ field.label }}</span>
                  <Tag v-if="field.required" color="red">必填</Tag>
                  <Tag v-if="field.blocking" color="volcano">阻塞启动</Tag>
                  <Tag v-if="field.deprecated" color="default">已废弃</Tag>
                  <Tag v-if="field.hot_whitelist" color="green">可热更</Tag>
                  <code class="text-xs text-slate-500">{{ field.key }}</code>
                </div>
                <p v-if="field.description" class="text-xs text-slate-500">
                  {{ field.description }}
                </p>
                <p v-if="defaultBadge(field)" class="text-xs text-blue-600">
                  {{ defaultBadge(field) }}
                </p>
              </div>
              <div class="space-y-1">
                <Switch
                  v-if="field.widget === 'switch'"
                  :checked="isOn(field)"
                  @change="(checked) => setFieldValue(field, Boolean(checked))"
                />
                <Select
                  v-else-if="field.widget === 'select' && field.enum?.length"
                  :value="fieldValue(field) || undefined"
                  class="w-full"
                  :options="
                    field.enum.map((item) => ({ label: item, value: item }))
                  "
                  :placeholder="
                    field.default.source === 'none'
                      ? '请选择'
                      : `默认 ${field.default.value}`
                  "
                  @change="(value) => setFieldValue(field, value as string)"
                />
                <InputNumber
                  v-else-if="field.widget === 'number'"
                  class="w-full"
                  :value="
                    fieldValue(field) ? Number(fieldValue(field)) : undefined
                  "
                  :min="field.min_int ?? undefined"
                  :max="field.max_int ?? undefined"
                  :placeholder="field.default.value || '整数'"
                  @change="(value) => setFieldValue(field, value ?? '')"
                />
                <Input.TextArea
                  v-else-if="field.widget === 'code'"
                  :rows="3"
                  class="font-mono text-xs"
                  :value="fieldValue(field)"
                  :placeholder="field.default.value || 'JSON'"
                  @input="
                    (event) => setFieldValue(field, inputValue(event as Event))
                  "
                />
                <Input
                  v-else
                  :type="field.widget === 'password' ? 'password' : 'text'"
                  :value="fieldValue(field)"
                  :placeholder="field.default.value || field.value_type"
                  @input="
                    (event) => setFieldValue(field, inputValue(event as Event))
                  "
                />
                <p class="text-xs text-slate-400">
                  <span v-if="isFieldFilled(field)">已填写（覆盖默认值）</span>
                  <span v-else>留空 = 使用默认值</span>
                </p>
              </div>
            </div>
          </div>
        </div>
      </section>

      <!-- 第 3 步：业务配置 -->
      <section v-show="step === 2" class="space-y-6">
        <Alert
          v-if="isRepair && businessSections.length === 0"
          type="info"
          show-icon
          message="repair 模式只允许修改阻塞键；非阻塞变更请走 提交 → 审批 → 发布"
        />
        <div
          v-for="section in businessSections"
          :key="section.namespace"
          class="space-y-4 rounded border p-4"
        >
          <header class="flex items-center gap-2">
            <h3 class="text-base font-semibold">{{ section.title }}</h3>
            <Tag v-if="section.blockingCount > 0" color="red">
              阻塞 {{ section.blockingCount }}
            </Tag>
          </header>
          <div
            v-for="group in section.groups"
            :key="group.id"
            class="space-y-2"
          >
            <h4 class="text-sm font-medium text-slate-600">
              {{ group.title }}
            </h4>
            <div
              v-for="field in group.fields"
              :key="field.key"
              class="flex flex-wrap items-center gap-2 text-sm"
            >
              <span class="w-64 shrink-0 font-medium">
                {{ field.label }}
                <Tag v-if="field.required" color="red">必填</Tag>
                <Tag v-if="field.blocking" color="volcano">阻塞</Tag>
              </span>
              <Switch
                v-if="field.widget === 'switch'"
                :checked="isOn(field)"
                @change="(checked) => setFieldValue(field, Boolean(checked))"
              />
              <Select
                v-else-if="field.widget === 'select' && field.enum?.length"
                class="min-w-64"
                :value="fieldValue(field) || undefined"
                :options="
                  field.enum.map((item) => ({ label: item, value: item }))
                "
                :placeholder="field.default.value || '请选择'"
                @change="(value) => setFieldValue(field, value as string)"
              />
              <InputNumber
                v-else-if="field.widget === 'number'"
                :value="
                  fieldValue(field) ? Number(fieldValue(field)) : undefined
                "
                :min="field.min_int ?? undefined"
                :max="field.max_int ?? undefined"
                :placeholder="field.default.value || '整数'"
                @change="(value) => setFieldValue(field, value ?? '')"
              />
              <Input
                v-else
                class="min-w-64"
                :type="field.widget === 'password' ? 'password' : 'text'"
                :value="fieldValue(field)"
                :placeholder="field.default.value || field.value_type"
                @input="
                  (event) => setFieldValue(field, inputValue(event as Event))
                "
              />
              <span v-if="defaultBadge(field)" class="text-xs text-blue-600">
                {{ defaultBadge(field) }}
              </span>
              <code class="text-xs text-slate-400">{{ field.key }}</code>
            </div>
          </div>
        </div>
      </section>

      <!-- 第 4 步：业务密钥 -->
      <section v-show="step === 3" class="space-y-4">
        <Alert
          type="info"
          show-icon
          message="敏感项由密钥服务在安装时自动生成，本页不收集也不回显密钥值"
        >
          <template #description>
            需要人工指定或轮换时，安装完成后到「配置中心 → 业务密钥」走提交 →
            审批 → 激活， 全程留审计；这里的规则只决定自动生成的长度与算法。
          </template>
        </Alert>
        <div v-if="secrets.length === 0" class="text-sm text-slate-500">
          当前环境没有敏感键声明。
        </div>
        <div
          v-for="field in secrets"
          :key="field.key"
          class="space-y-1 rounded border p-3 text-sm"
        >
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-medium">{{ field.label }}</span>
            <Tag color="purple">敏感</Tag>
            <Tag v-if="field.secret?.auto_generate" color="green">自动生成</Tag>
            <code class="text-xs text-slate-500">{{ field.key }}</code>
          </div>
          <div v-if="field.secret" class="text-xs text-slate-500">
            算法 {{ field.secret.algorithm || '—' }} · 编码
            {{ field.secret.encoding || '—' }} · 长度
            {{ field.secret.min_len }}–{{ field.secret.max_len }}
            <span v-if="field.secret.prefix">
              · 前缀 {{ field.secret.prefix }}
            </span>
          </div>
          <p v-if="field.description" class="text-xs text-slate-500">
            {{ field.description }}
          </p>
        </div>
      </section>

      <!-- 第 5 步：预览与提交 -->
      <section v-show="step === 4" class="space-y-4">
        <div class="rounded border p-4 text-sm">
          <div>
            本次将写入 {{ filledCount(draft) }} 个显式值，其余使用默认值播种。
          </div>
          <div v-if="missingRequired.length > 0" class="text-red-600">
            还缺：
            {{
              missingRequired
                .map((field) => field.label || field.key)
                .join('、')
            }}
          </div>
          <div v-if="remainingGaps.length > 0" class="text-red-600">
            未满足的阻塞键：
            {{
              remainingGaps
                .map((gap) => `${gap.namespace}/${gap.key}`)
                .join('、')
            }}
          </div>
        </div>
        <Space>
          <Button
            type="primary"
            :disabled="!canSubmit"
            :loading="busy"
            @click="runInstall"
          >
            {{ isRepair ? '提交修复并完成' : '开始并完成安装' }}
          </Button>
          <Button :disabled="busy" @click="refreshStatus">刷新状态</Button>
        </Space>
      </section>

      <div class="flex items-center justify-between">
        <Space>
          <Button :disabled="step === 0" @click="step -= 1">上一步</Button>
          <Button :disabled="step >= 4" @click="step += 1">下一步</Button>
        </Space>
        <span class="text-xs text-slate-400">
          向导不改非阻塞键；非阻塞变更始终走 提交 → 审批 → 发布
        </span>
      </div>

      <p v-if="message" class="text-sm text-green-700">{{ message }}</p>
      <p v-if="errorMessage" class="text-sm text-red-600">{{ errorMessage }}</p>
    </div>
  </Page>
</template>
