<script setup lang="ts">
import type { SecuritySchemaItem } from '@vben/common-ui';

import type { MeView } from '#/api';

import { computed, onMounted, ref } from 'vue';

import { ProfileSecuritySetting } from '@vben/common-ui';

import { Alert, Button } from 'ant-design-vue';
import dayjs from 'dayjs';

import { getMeApi } from '#/api';
import { $t } from '#/locales';

import MfaSetting from './mfa-setting.vue';

const emit = defineEmits<{
  /** 请求父级切换到其他页签（如修改密码）。 */
  changeTab: [value: string];
}>();

const loading = ref(true);
const loadFailed = ref(false);
const me = ref<MeView | null>(null);
const mfaSettingRef = ref<{
  openBind: () => void;
  openUnbind: () => void;
}>();

/**
 * 拉取当前主体资料。
 *
 * 失败时保持空态并提示重试：绝不能把请求失败当成「未绑定」渲染，
 * 那会把不可用的安全状态伪装成结论。
 */
async function loadMe() {
  loading.value = true;
  loadFailed.value = false;
  try {
    // 页面自带错误提示与重试入口，跳过全局 toast，避免重复打扰。
    me.value = await getMeApi({ skipErrorMessage: true });
  } catch {
    me.value = null;
    loadFailed.value = true;
  } finally {
    loading.value = false;
  }
}

onMounted(loadMe);

const passwordDescription = computed(() => {
  const data = me.value;
  if (!data?.has_password) {
    return $t('profile.securityPasswordUnsetDesc');
  }
  if (!data.password_updated_at) {
    return $t('profile.securityPasswordNeverUpdated');
  }
  return $t('profile.securityPasswordUpdatedAt', {
    time: dayjs(data.password_updated_at).format('YYYY-MM-DD HH:mm'),
  });
});

const formSchema = computed((): SecuritySchemaItem[] => {
  const data = me.value;
  // 未加载完成或加载失败时不传任何条目，由 loading / error 分支承载展示。
  if (!data) {
    return [];
  }
  return [
    {
      fieldName: 'accountPassword',
      label: $t('profile.securityAccountPassword'),
      status: data.has_password ? 'bound' : 'unbound',
      statusLabel: data.has_password
        ? $t('profile.securityStatusSet')
        : $t('profile.securityStatusUnset'),
      description: passwordDescription.value,
      // 空字符串表示无操作入口（未设置密码时自助改密不可用）
      actionLabel: data.has_password
        ? $t('profile.securityChangePassword')
        : '',
    },
    {
      fieldName: 'securityPhone',
      label: $t('profile.securityPhone'),
      status: data.phone_bound ? 'bound' : 'unbound',
      description: data.phone_bound
        ? $t('profile.securityPhoneBoundDesc')
        : $t('profile.securityPhoneUnboundDesc'),
    },
    {
      fieldName: 'securityQuestion',
      label: $t('profile.securityQuestion'),
      status: 'unavailable',
      description: $t('profile.securityNotAvailableDesc'),
    },
    {
      fieldName: 'securityEmail',
      label: $t('profile.securityEmail'),
      status: 'unavailable',
      description: $t('profile.securityNotAvailableDesc'),
    },
    {
      fieldName: 'securityMfa',
      label: $t('profile.mfaDevice'),
      status: data.mfa_enabled ? 'bound' : 'unbound',
      description: data.mfa_enabled
        ? $t('profile.mfaBoundDesc')
        : $t('profile.mfaUnboundDesc'),
      actionLabel: data.mfa_enabled
        ? $t('profile.mfaUnbind')
        : $t('profile.mfaBind'),
    },
  ];
});

function handleAction(fieldName: string) {
  if (fieldName === 'accountPassword') {
    emit('changeTab', 'password');
    return;
  }
  if (fieldName === 'securityMfa') {
    if (me.value?.mfa_enabled) {
      mfaSettingRef.value?.openUnbind();
    } else {
      mfaSettingRef.value?.openBind();
    }
  }
}
</script>
<template>
  <div class="space-y-6">
    <Alert
      v-if="me?.must_change_password"
      :message="$t('profile.securityMustChangePassword')"
      class="mb-4"
      show-icon
      type="warning"
    />

    <div v-if="loadFailed" class="mb-4 flex items-center gap-3">
      <Alert
        class="flex-1"
        :message="$t('profile.securityProfileLoadFailed')"
        show-icon
        type="error"
      />
      <Button size="small" @click="loadMe">
        {{ $t('profile.securityRetry') }}
      </Button>
    </div>

    <template v-else>
      <ProfileSecuritySetting
        :form-schema="formSchema"
        :loading="loading"
        @action="handleAction"
      />
      <MfaSetting ref="mfaSettingRef" @refresh="loadMe" />
    </template>
  </div>
</template>
