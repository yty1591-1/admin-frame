<script setup lang="ts">
import type { SecuritySchemaItem } from '@vben/common-ui';

import type { MeView } from '#/api';

import { computed, onMounted, ref } from 'vue';

import { ProfileSecuritySetting } from '@vben/common-ui';

import { Alert, Button } from 'ant-design-vue';
import dayjs from 'dayjs';

import { getMeApi } from '#/api';
import { $t } from '#/locales';

const loading = ref(true);
/** 资料接口不可用：B 端认证尚未迁移到真实契约（FEREV-04）。 */
const unavailable = ref(false);
const me = ref<MeView | null>(null);

/**
 * 拉取当前主体资料。
 *
 * B 端 mock 环境没有 `/me`，请求必然失败；此时整页降级为「暂不可用」，
 * 绝不把失败渲染成「未绑定」。
 */
async function loadMe() {
  loading.value = true;
  unavailable.value = false;
  try {
    me.value = await getMeApi({ skipErrorMessage: true });
  } catch {
    me.value = null;
    unavailable.value = true;
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
  const unavailableDesc = $t('profile.securityProfileUnavailable');

  if (!data) {
    return [
      {
        fieldName: 'accountPassword',
        label: $t('profile.securityAccountPassword'),
        status: 'unavailable',
        description: unavailableDesc,
      },
      {
        fieldName: 'securityPhone',
        label: $t('profile.securityPhone'),
        status: 'unavailable',
        description: unavailableDesc,
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
        status: 'unavailable',
        description: $t('profile.securityMfaUnavailableDesc'),
      },
    ];
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
      status: 'unavailable',
      description: $t('profile.securityMfaUnavailableDesc'),
    },
  ];
});
</script>
<template>
  <div class="space-y-6">
    <div v-if="unavailable" class="flex items-center gap-3">
      <Alert
        class="flex-1"
        :message="$t('profile.securityProfileUnavailable')"
        show-icon
        type="info"
      />
      <Button size="small" @click="loadMe">
        {{ $t('profile.securityRetry') }}
      </Button>
    </div>

    <ProfileSecuritySetting :form-schema="formSchema" :loading="loading" />
  </div>
</template>
