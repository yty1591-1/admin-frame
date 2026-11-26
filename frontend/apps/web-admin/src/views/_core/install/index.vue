<script lang="ts" setup>
import type { RailMetaRow, RailStep } from './components/StepRail.vue';
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

import { Alert, Button, Input, Tag } from 'ant-design-vue';

import {
  applyInstall,
  beginInstall,
  completeInstall,
  fetchInstallSchema,
  fetchInstallStatus,
} from '#/api/configcenter/install';
import { $t } from '#/locales';

import ConfigSections from './components/ConfigSections.vue';
import GuidePanel from './components/GuidePanel.vue';
import SecretList from './components/SecretList.vue';
import StepRail from './components/StepRail.vue';
import {
  buildSections,
  collectValues,
  configFields,
  declaredKeyRefs,
  filledCount,
  filledEntries,
  missingFields,
  secretFields,
  writableSecretFields,
} from './schema';

const router = useRouter();

/** 阻塞原因文案（与 OpenAPI InstallGap.reason 对齐）。 */
function gapReasonLabel(reason: string | undefined): string {
  switch (reason) {
    case 'no_active_release': {
      return '命名空间尚无生效发布';
    }
    case 'missing_in_snapshot': {
      return '快照缺键';
    }
    case 'connectivity_failed': {
      return '连通性探测失败';
    }
    case 'deploy_env_missing': {
      return '需运维补部署环境变量（如 ADMIN_FRAME_DB_DSN）';
    }
    case 'secret_missing': {
      return '密钥子域缺失';
    }
    default: {
      return reason || '';
    }
  }
}

const step = ref(0);
const token = ref('');
const owner = ref('');
const declaredText = ref('');
const busy = ref(false);
const message = ref('');
const errorMessage = ref('');
/** 移动端运行上下文折叠面板。 */
const metaOpen = ref(false);

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
const writableSecrets = computed<SchemaField[]>(() =>
  schema.value ? writableSecretFields(schema.value) : [],
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
const deployGaps = computed(() =>
  gaps.value.filter((gap) => gap.reason === 'deploy_env_missing'),
);
const otherGaps = computed(() =>
  gaps.value.filter((gap) => gap.reason !== 'deploy_env_missing'),
);
const missingRequired = computed(() =>
  missingFields(formFields.value, draft, mode.value),
);
const previewEntries = computed(() => filledEntries(draft));

const canSubmit = computed(
  () =>
    !busy.value &&
    !!token.value &&
    !!schema.value &&
    missingRequired.value.length === 0,
);

const railSteps = computed<RailStep[]>(() => [
  {
    key: 'token',
    title: $t('install.stepToken'),
    hint: $t('install.stepTokenHint'),
  },
  {
    key: 'platform',
    title: $t('install.stepPlatform'),
    hint: $t('install.stepPlatformHint'),
  },
  {
    key: 'business',
    title: $t('install.stepBusiness'),
    hint: $t('install.stepBusinessHint'),
  },
  {
    key: 'secrets',
    title: $t('install.stepSecrets'),
    hint: $t('install.stepSecretsHint'),
  },
  {
    key: 'review',
    title: $t('install.stepReview'),
    hint: $t('install.stepReviewHint'),
  },
]);

const railMetaRows = computed<RailMetaRow[]>(() => {
  const st = status.value?.status || '—';
  let statusTone: RailMetaRow['tone'] = 'muted';
  if (st === 'ready') statusTone = 'success';
  else if (st === 'installing') statusTone = 'primary';
  else if (st === 'failed' || st === 'pending') statusTone = 'danger';

  return [
    {
      label: $t('install.metaEnv'),
      value: status.value?.env || schema.value?.env || '—',
    },
    { label: $t('install.metaStatus'), value: st, tone: statusTone },
    {
      label: $t('install.metaMode'),
      value: isRepair.value
        ? $t('install.modeRepair')
        : $t('install.modeFirst'),
      tone: isRepair.value ? 'warning' : 'primary',
    },
    {
      label: $t('install.metaBlocking'),
      value: String(blockingCount.value),
      tone: blockingCount.value > 0 ? 'danger' : 'success',
    },
    ...(status.value?.owner || owner.value
      ? [
          {
            label: $t('install.metaOwner'),
            value: status.value?.owner || owner.value,
          },
        ]
      : []),
  ];
});

const stepTitle = computed(() => {
  if (step.value === 0) {
    return isRepair.value
      ? $t('install.titleTokenRepair')
      : $t('install.titleToken');
  }
  const titles = [
    '',
    $t('install.titlePlatform'),
    $t('install.titleBusiness'),
    $t('install.titleSecrets'),
    $t('install.titleReview'),
  ];
  return titles[step.value] || '';
});

const stepDesc = computed(() => {
  if (step.value === 0) {
    return isRepair.value
      ? $t('install.descTokenRepair')
      : $t('install.descToken');
  }
  const descriptions = [
    '',
    $t('install.descPlatform'),
    $t('install.descBusiness'),
    $t('install.descSecrets'),
    $t('install.descReview'),
  ];
  return descriptions[step.value] || '';
});

const stepProgress = computed(() => {
  const title = railSteps.value[step.value]?.title || '';
  if (isRepair.value) {
    return $t('install.progressRepair', {
      current: step.value + 1,
      total: 5,
    });
  }
  return $t('install.progress', {
    current: step.value + 1,
    total: 5,
    title,
  });
});

const guideItems = computed(() => {
  if (step.value === 0) {
    return isRepair.value
      ? [
          $t('install.guideRepair1'),
          $t('install.guideRepair2'),
          $t('install.guideRepair3'),
        ]
      : [
          $t('install.guideToken1'),
          $t('install.guideToken2'),
          $t('install.guideToken3'),
          $t('install.guideToken4'),
        ];
  }
  if (step.value === 1) {
    return [
      $t('install.guidePlatform1'),
      $t('install.guidePlatform2'),
      $t('install.guidePlatform3'),
      $t('install.guidePlatform4'),
    ];
  }
  if (step.value === 2) {
    return [
      $t('install.guideBusiness1'),
      $t('install.guideBusiness2'),
      $t('install.guideBusiness3'),
    ];
  }
  if (step.value === 3) {
    return [
      $t('install.guideSecrets1'),
      $t('install.guideSecrets2'),
      $t('install.guideSecrets3'),
    ];
  }
  return [
    $t('install.guideReview1'),
    $t('install.guideReview2'),
    $t('install.guideReview3'),
  ];
});

const primaryLabel = computed(() => {
  if (step.value === 0) return $t('install.btnLoadSchema');
  if (step.value === 3) return $t('install.btnPreview');
  if (step.value === 4) {
    return isRepair.value
      ? $t('install.btnSubmitRepair')
      : $t('install.btnSubmit');
  }
  return $t('install.btnContinue');
});

async function refreshStatus() {
  errorMessage.value = '';
  try {
    status.value = await fetchInstallStatus();
    if (status.value.status === 'ready') {
      message.value = $t('install.msgReady');
    }
  } catch (error) {
    errorMessage.value = describe(error);
  }
}

async function loadSchema() {
  if (!token.value) {
    errorMessage.value = $t('install.msgNeedToken');
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
        ? $t('install.msgSchemaRepair', {
            count: schema.value.blocking_count,
          })
        : $t('install.msgSchemaLoaded', {
            fields: schema.value.fields.length,
            namespaces: schema.value.namespaces.length,
          });
    step.value = 1;
  } catch (error) {
    errorMessage.value = describe(error);
  } finally {
    busy.value = false;
  }
}

async function runInstall() {
  if (missingRequired.value.length > 0) {
    errorMessage.value = $t('install.msgMissingRequired', {
      list: missingRequired.value
        .map((field) => field.label || field.key)
        .join('、'),
    });
    return;
  }
  busy.value = true;
  errorMessage.value = '';
  message.value = '';
  try {
    const values = collectValues(
      [...formFields.value, ...writableSecrets.value],
      draft,
    );
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
    message.value = $t('install.msgApplied', {
      items: applied.item_count,
      namespaces: applied.namespaces,
    });

    if (applied.remaining_gaps.length > 0) {
      errorMessage.value = $t('install.msgRemaining', {
        count: applied.remaining_gaps.length,
      });
      step.value = 4;
      return;
    }
    const done = await completeInstall(token.value, owner.value);
    status.value = { ...done, blocking_count: 0, gaps: [] };
    message.value = $t('install.msgCompleted');
    step.value = 4;
  } catch (error) {
    errorMessage.value = describe(error);
    await refreshStatus();
  } finally {
    busy.value = false;
  }
}

function setDraftValue(namespace: string, key: string, value: string) {
  draft[namespace] ??= {};
  const group = draft[namespace];
  if (group) {
    group[key] = value;
  }
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function onPrimary() {
  if (step.value === 0) {
    void loadSchema();
    return;
  }
  if (step.value === 4) {
    void runInstall();
    return;
  }
  if (step.value < 4) {
    step.value += 1;
    metaOpen.value = false;
  }
}

function goPrev() {
  if (step.value > 0) {
    step.value -= 1;
    metaOpen.value = false;
  }
}

onMounted(refreshStatus);
</script>

<template>
  <div
    class="bg-background-deep text-foreground flex h-dvh min-h-0 w-full flex-col overflow-hidden lg:min-h-[640px] lg:flex-row"
  >
    <StepRail
      v-model:meta-open="metaOpen"
      :context-title="$t('install.contextTitle')"
      :current="step"
      :last-error="status?.last_error"
      :last-error-prefix="$t('install.lastErrorPrefix')"
      :meta-rows="railMetaRows"
      :steps="railSteps"
      :subtitle="$t('install.brandSubtitle')"
      :title="$t('install.brandTitle')"
    />

    <main class="flex min-h-0 min-w-0 flex-1 flex-col p-0 sm:p-4 lg:p-7">
      <div
        class="border-border bg-card flex min-h-0 flex-1 flex-col overflow-hidden border-0 sm:rounded-lg sm:border"
      >
        <div
          class="min-h-0 flex-1 space-y-4 overflow-y-auto overscroll-contain px-4 py-5 sm:space-y-5 sm:px-6 sm:py-6 lg:px-8 lg:py-7"
        >
          <header class="space-y-1.5">
            <p class="text-primary text-xs font-medium">
              {{ stepProgress }}
            </p>
            <h1
              class="text-foreground text-xl font-semibold tracking-tight sm:text-[22px]"
            >
              {{ stepTitle }}
            </h1>
            <p
              class="text-muted-foreground max-w-3xl text-[13px] leading-relaxed"
            >
              {{ stepDesc }}
            </p>
          </header>

          <GuidePanel :items="guideItems" :title="$t('install.guideTitle')" />

          <Alert
            v-if="deployGaps.length > 0"
            type="error"
            show-icon
            :message="$t('install.alertDeployTitle')"
          >
            <template #description>
              {{ $t('install.alertDeployDesc') }}
            </template>
          </Alert>

          <Alert
            v-if="otherGaps.length > 0 && step <= 1"
            type="warning"
            show-icon
            :message="$t('install.alertGapsTitle', { count: otherGaps.length })"
          >
            <template #description>
              <ul class="list-disc pl-4">
                <li
                  v-for="gap in otherGaps.slice(0, 8)"
                  :key="`${gap.namespace}/${gap.key}`"
                >
                  <code>{{ gap.namespace }}/{{ gap.key }}</code>
                  <span v-if="gap.reason" class="text-muted-foreground">
                    （{{ gapReasonLabel(gap.reason) }}）
                  </span>
                </li>
                <li v-if="otherGaps.length > 8">
                  …共 {{ otherGaps.length }} 项
                </li>
              </ul>
            </template>
          </Alert>

          <!-- 第 1 步：令牌 -->
          <section v-show="step === 0" class="max-w-xl space-y-4">
            <label class="block space-y-1.5 text-sm">
              <span class="text-foreground flex items-center gap-2 font-medium">
                {{ $t('install.tokenLabel') }}
                <Tag color="red">{{ $t('install.required') }}</Tag>
              </span>
              <Input
                v-model:value="token"
                type="password"
                autocomplete="off"
                :placeholder="$t('install.tokenPlaceholder')"
              />
              <span class="text-muted-foreground text-xs leading-relaxed">
                {{ $t('install.tokenHelp') }}
              </span>
            </label>

            <label class="block space-y-1.5 text-sm">
              <span class="text-foreground font-medium">
                {{ $t('install.ownerLabel') }}
              </span>
              <Input
                v-model:value="owner"
                :placeholder="$t('install.ownerPlaceholder')"
              />
              <span class="text-muted-foreground text-xs leading-relaxed">
                {{ $t('install.ownerHelp') }}
              </span>
            </label>

            <label v-if="isRepair" class="block space-y-1.5 text-sm">
              <span class="text-foreground font-medium">
                {{ $t('install.declaredLabel') }}
              </span>
              <Input.TextArea
                v-model:value="declaredText"
                :rows="3"
                :placeholder="$t('install.declaredPlaceholder')"
                class="font-mono text-xs"
              />
              <span class="text-muted-foreground text-xs leading-relaxed">
                {{ $t('install.declaredHelp') }}
              </span>
            </label>
          </section>

          <!-- 第 2 步：平台 -->
          <section v-show="step === 1" class="space-y-4">
            <Alert
              v-if="isRepair && platformSections.length === 0"
              type="info"
              show-icon
              :message="$t('install.alertRepairEmptyPlatform')"
            />
            <ConfigSections
              :draft="draft"
              :empty-message="$t('install.alertRepairEmptyPlatform')"
              :sections="platformSections"
              @set-value="setDraftValue"
            />
          </section>

          <!-- 第 3 步：业务 -->
          <section v-show="step === 2" class="space-y-4">
            <Alert
              v-if="isRepair && businessSections.length === 0"
              type="info"
              show-icon
              :message="$t('install.alertRepairEmptyBusiness')"
            />
            <ConfigSections
              compact
              :draft="draft"
              :empty-message="$t('install.alertRepairEmptyBusiness')"
              :sections="businessSections"
              @set-value="setDraftValue"
            />
          </section>

          <!-- 第 4 步：密钥 -->
          <section v-show="step === 3" class="space-y-4">
            <Alert
              type="info"
              show-icon
              :message="$t('install.alertSecretsTitle')"
            >
              <template #description>
                {{ $t('install.alertSecretsDesc') }}
              </template>
            </Alert>
            <SecretList
              :draft="draft"
              :fields="secrets"
              @set-value="setDraftValue"
            >
              <template #empty>
                {{ $t('install.secretsEmpty') }}
              </template>
            </SecretList>
          </section>

          <!-- 第 5 步：预览 -->
          <section v-show="step === 4" class="space-y-4">
            <div
              class="border-border bg-muted overflow-hidden rounded-lg border"
            >
              <div
                class="border-border flex items-center justify-between border-b px-3.5 py-3"
              >
                <span class="text-sm font-semibold">
                  {{
                    $t('install.summaryTitle', {
                      count: filledCount(draft),
                    })
                  }}
                </span>
                <span class="text-muted-foreground text-xs">
                  {{ $t('install.summaryHint') }}
                </span>
              </div>
              <div
                v-if="previewEntries.length === 0"
                class="text-muted-foreground px-3.5 py-4 text-sm"
              >
                —
              </div>
              <div
                v-for="(row, index) in previewEntries"
                :key="`${row.namespace}/${row.key}`"
                class="flex flex-col gap-1 px-3.5 py-3 text-sm sm:flex-row sm:items-center sm:gap-3"
                :class="
                  index < previewEntries.length - 1
                    ? 'border-border border-b'
                    : ''
                "
              >
                <span class="text-muted-foreground shrink-0 text-xs sm:w-16">
                  {{ row.namespace }}
                </span>
                <code
                  class="text-foreground break-all font-mono text-xs sm:w-52 sm:shrink-0"
                >
                  {{ row.key }}
                </code>
                <span class="text-foreground break-all font-medium">
                  {{
                    secrets.some(
                      (s) => s.namespace === row.namespace && s.key === row.key,
                    )
                      ? '••••••••'
                      : row.value
                  }}
                </span>
              </div>
            </div>

            <p
              v-if="missingRequired.length > 0"
              class="text-destructive text-sm"
            >
              {{
                $t('install.summaryMissing', {
                  list: missingRequired
                    .map((field) => field.label || field.key)
                    .join('、'),
                })
              }}
            </p>
            <p v-if="remainingGaps.length > 0" class="text-destructive text-sm">
              {{
                $t('install.summaryRemaining', {
                  list: remainingGaps
                    .map((gap) => `${gap.namespace}/${gap.key}`)
                    .join('、'),
                })
              }}
            </p>

            <Alert
              v-if="canSubmit && remainingGaps.length === 0"
              type="success"
              show-icon
              :message="$t('install.alertReadyTitle')"
            >
              <template #description>
                {{ $t('install.alertReadyDesc') }}
              </template>
            </Alert>
          </section>

          <p v-if="message" class="text-success text-sm">{{ message }}</p>
          <p v-if="errorMessage" class="text-destructive text-sm">
            {{ errorMessage }}
          </p>
        </div>

        <footer
          class="border-border flex shrink-0 flex-col gap-3 border-t px-4 py-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:flex-row sm:items-center sm:justify-between sm:px-6 sm:py-4 lg:px-8"
        >
          <Button
            v-if="step === 0"
            type="link"
            class="self-start px-0"
            @click="router.push('/auth/login')"
          >
            {{ $t('install.btnLogin') }}
          </Button>
          <Button
            v-else
            type="link"
            class="self-start px-0"
            :disabled="busy"
            @click="goPrev"
          >
            {{ $t('install.btnPrev') }}
          </Button>
          <div class="flex w-full flex-col-reverse gap-2 sm:w-auto sm:flex-row">
            <Button
              class="w-full sm:w-auto"
              :disabled="busy"
              @click="refreshStatus"
            >
              {{ $t('install.btnRefresh') }}
            </Button>
            <Button
              type="primary"
              class="w-full sm:w-auto"
              :loading="busy"
              :disabled="step === 4 && !canSubmit"
              @click="onPrimary"
            >
              {{ primaryLabel }}
            </Button>
          </div>
        </footer>
      </div>
    </main>
  </div>
</template>
